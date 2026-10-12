package nats

import (
	"context"
	"sync"
	"time"
)

const DefaultMaxAge = 24 * time.Hour

type entry struct {
	channel   string
	data      []byte
	timestamp time.Time
}

type RingBuffer struct {
	mu           sync.RWMutex
	entries      []entry
	maxSize      int
	maxBytes     int
	currentBytes int
	maxAge       time.Duration
	channelTTLs  map[string]time.Duration
	head         int
	tail         int
	full         bool
}

// NewRingBuffer creates a ring buffer bounded by entry count (maxSize) and
// retained payload bytes (maxBytes; 0 = unbounded) with a global maxAge.
func NewRingBuffer(maxSize, maxBytes int, maxAge time.Duration) *RingBuffer {
	if maxSize <= 0 {
		maxSize = 1
	}
	return &RingBuffer{
		entries:     make([]entry, maxSize),
		maxSize:     maxSize,
		maxBytes:    maxBytes,
		maxAge:      maxAge,
		channelTTLs: make(map[string]time.Duration),
	}
}

func (rb *RingBuffer) SetChannelTTL(channel string, ttl time.Duration) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if ttl <= 0 {
		delete(rb.channelTTLs, channel)
	} else {
		rb.channelTTLs[channel] = ttl
	}
}

// len returns the number of live entries. Callers hold rb.mu.
func (rb *RingBuffer) lenLocked() int {
	if rb.full {
		return rb.maxSize
	}
	if rb.head >= rb.tail {
		return rb.head - rb.tail
	}
	return rb.maxSize - rb.tail + rb.head
}

// evictOldestLocked drops the oldest entry and zeroes its payload so the
// backing array no longer pins the retained bytes. Callers hold rb.mu.
func (rb *RingBuffer) evictOldestLocked() {
	if rb.lenLocked() == 0 {
		return
	}
	e := &rb.entries[rb.tail]
	rb.currentBytes -= len(e.data)
	for i := range e.data {
		e.data[i] = 0
	}
	*e = entry{}
	rb.tail = (rb.tail + 1) % rb.maxSize
	if rb.tail == rb.head {
		rb.full = false
	}
}

// Append adds a payload, evicting oldest entries to satisfy the size and byte
// budgets.
func (rb *RingBuffer) Append(channel string, data []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.maxBytes > 0 {
		for rb.lenLocked() > 0 && rb.currentBytes+len(data) > rb.maxBytes {
			rb.evictOldestLocked()
		}
	}
	if rb.full {
		rb.evictOldestLocked()
	}

	rb.entries[rb.head] = entry{
		channel:   channel,
		data:      data,
		timestamp: time.Now(),
	}
	rb.currentBytes += len(data)

	next := (rb.head + 1) % rb.maxSize
	if next == rb.tail {
		rb.full = true
	}
	rb.head = next
}

// entryExpired reports whether e is past its effective (per-channel or global)
// TTL.
func (rb *RingBuffer) entryExpired(e entry, now time.Time) bool {
	if e.timestamp.IsZero() {
		return true
	}
	age := rb.maxAge
	if chTTL, ok := rb.channelTTLs[e.channel]; ok && chTTL > 0 && (age <= 0 || chTTL < age) {
		age = chTTL
	}
	if age <= 0 {
		return false
	}
	return !e.timestamp.After(now.Add(-age))
}

// Get returns up to limit payloads for channel newer than since, skipping
// expired entries. It scans the whole buffer so per-channel TTLs interleaved
// with other channels are honored.
func (rb *RingBuffer) Get(channel string, since time.Time, limit int) [][]byte {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	n := rb.lenLocked()
	if n == 0 {
		return nil
	}
	now := time.Now()
	result := make([][]byte, 0)
	for i := 0; i < n; i++ {
		e := rb.entries[(rb.tail+i)%rb.maxSize]
		if e.channel != channel || !e.timestamp.After(since) {
			continue
		}
		if rb.entryExpired(e, now) {
			continue
		}
		result = append(result, e.data)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

// RetainedBytes returns the number of payload bytes currently retained.
func (rb *RingBuffer) RetainedBytes() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.currentBytes
}

func (rb *RingBuffer) startCleanup(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rb.evictExpired()
		}
	}
}

// evictExpired rebuilds the ring without expired entries. It iterates the full
// live set (no early break) because per-channel TTLs make expiry
// non-monotonic.
func (rb *RingBuffer) evictExpired() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	n := rb.lenLocked()
	if n == 0 {
		return
	}
	now := time.Now()
	kept := make([]entry, 0, n)
	for i := 0; i < n; i++ {
		e := rb.entries[(rb.tail+i)%rb.maxSize]
		if rb.entryExpired(e, now) {
			rb.currentBytes -= len(e.data)
			continue
		}
		kept = append(kept, e)
	}

	for i := range rb.entries {
		rb.entries[i] = entry{}
	}
	rb.tail = 0
	rb.full = len(kept) == rb.maxSize
	rb.head = len(kept) % rb.maxSize
	for i, e := range kept {
		rb.entries[i] = e
	}
}
