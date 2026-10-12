package nats

import (
	"bytes"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// F11: the ring buffer evicts to stay under the byte budget.
func TestByteBudgetRetention(t *testing.T) {
	rb := NewRingBuffer(100, 50, time.Hour)

	rb.Append("c", bytes.Repeat([]byte("a"), 20))
	rb.Append("c", bytes.Repeat([]byte("b"), 20))
	assert.Equal(t, 40, rb.RetainedBytes())

	// 60 > 50: the oldest entry must be evicted before adding.
	rb.Append("c", bytes.Repeat([]byte("c"), 20))
	assert.Assert(t, rb.RetainedBytes() <= 50, "retained bytes %d exceed budget", rb.RetainedBytes())
	assert.Equal(t, 2, len(rb.Get("c", time.Time{}, 0)))
}

// F24: per-channel TTL must be honored even when entries of other channels are
// interleaved (the old eviction stopped at the first non-expired entry).
func TestTTLExpiryIndependentOfInterleaving(t *testing.T) {
	rb := NewRingBuffer(10, 0, time.Hour)
	rb.SetChannelTTL("short", 50*time.Millisecond)

	rb.Append("short", []byte("oldold"))
	rb.Append("long", []byte("keep"))
	time.Sleep(80 * time.Millisecond)
	rb.Append("short", []byte("new"))

	rb.evictExpired()

	assert.Equal(t, 7, rb.RetainedBytes(), "expired interleaved entry must be evicted")

	got := rb.Get("short", time.Time{}, 0)
	assert.Equal(t, 1, len(got))
	assert.Equal(t, "new", string(got[0]))

	kept := rb.Get("long", time.Time{}, 0)
	assert.Equal(t, 1, len(kept))
	assert.Equal(t, "keep", string(kept[0]))
}
