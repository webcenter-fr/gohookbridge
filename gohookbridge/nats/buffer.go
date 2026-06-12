package nats

import (
	"context"
	"sync"
	"time"
)

type entry struct {
	channel   string
	data      []byte
	timestamp time.Time
}

type RingBuffer struct {
	mu      sync.RWMutex
	entries []entry
	maxSize int
	maxAge  time.Duration
	head    int
	tail    int
	full    bool
}

func NewRingBuffer(maxSize int, maxAge time.Duration) *RingBuffer {
	return &RingBuffer{
		entries: make([]entry, maxSize),
		maxSize: maxSize,
		maxAge:  maxAge,
	}
}

func (rb *RingBuffer) Append(channel string, data []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.full {
		rb.tail = (rb.tail + 1) % rb.maxSize
	}
	rb.entries[rb.head] = entry{
		channel:   channel,
		data:      data,
		timestamp: time.Now(),
	}
	next := (rb.head + 1) % rb.maxSize
	if next == rb.tail {
		rb.full = true
	}
	rb.head = next
}

func (rb *RingBuffer) Get(channel string, since time.Time, limit int) [][]byte {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if !rb.full && rb.head == rb.tail {
		return nil
	}

	var result [][]byte
	count := 0
	if rb.tail < rb.head {
		for i := rb.tail; i < rb.head; i++ {
			e := rb.entries[i]
			if e.channel == channel && e.timestamp.After(since) {
				result = append(result, e.data)
				count++
				if limit > 0 && count >= limit {
					break
				}
			}
		}
	} else {
		for i := rb.tail; i < rb.maxSize; i++ {
			e := rb.entries[i]
			if e.channel == channel && e.timestamp.After(since) {
				result = append(result, e.data)
				count++
				if limit > 0 && count >= limit {
					goto done
				}
			}
		}
		for i := 0; i < rb.head; i++ {
			e := rb.entries[i]
			if e.channel == channel && e.timestamp.After(since) {
				result = append(result, e.data)
				count++
				if limit > 0 && count >= limit {
					break
				}
			}
		}
	}
done:
	return result
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

func (rb *RingBuffer) evictExpired() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if !rb.full && rb.head == rb.tail {
		return
	}

	cutoff := time.Now().Add(-rb.maxAge)
	var total int
	if rb.tail < rb.head {
		total = rb.head - rb.tail
	} else {
		total = rb.maxSize - rb.tail + rb.head
	}

	evicted := 0
	for i := 0; i < total; i++ {
		idx := (rb.tail + i) % rb.maxSize
		if rb.entries[idx].timestamp.IsZero() || rb.entries[idx].timestamp.After(cutoff) {
			break
		}
		evicted++
	}
	if evicted > 0 {
		rb.tail = (rb.tail + evicted) % rb.maxSize
		if rb.tail == rb.head {
			rb.full = false
		}
	}
}