package nats

import (
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestRingBufferAppendAndGet(t *testing.T) {
	rb := NewRingBuffer(5, time.Hour)

	rb.Append("ch1", []byte("data1"))
	rb.Append("ch1", []byte("data2"))
	rb.Append("ch2", []byte("data3"))

	historical := rb.Get("ch1", time.Time{}, 0)
	assert.Equal(t, 2, len(historical))
	assert.Equal(t, "data1", string(historical[0]))
	assert.Equal(t, "data2", string(historical[1]))

	historical = rb.Get("ch2", time.Time{}, 0)
	assert.Equal(t, 1, len(historical))
	assert.Equal(t, "data3", string(historical[0]))
}

func TestRingBufferSizeLimitEviction(t *testing.T) {
	rb := NewRingBuffer(3, time.Hour)

	rb.Append("ch1", []byte("data1"))
	rb.Append("ch1", []byte("data2"))
	rb.Append("ch1", []byte("data3"))
	rb.Append("ch1", []byte("data4"))

	historical := rb.Get("ch1", time.Time{}, 0)
	assert.Equal(t, 3, len(historical))
	assert.Equal(t, "data2", string(historical[0]))
	assert.Equal(t, "data3", string(historical[1]))
	assert.Equal(t, "data4", string(historical[2]))
}

func TestRingBufferTTLEviction(t *testing.T) {
	rb := NewRingBuffer(100, 50*time.Millisecond)

	rb.Append("ch1", []byte("data1"))
	time.Sleep(100 * time.Millisecond)
	rb.evictExpired()

	historical := rb.Get("ch1", time.Time{}, 0)
	assert.Equal(t, 0, len(historical))
}

func TestRingBufferGetLimit(t *testing.T) {
	rb := NewRingBuffer(100, time.Hour)

	for i := 0; i < 10; i++ {
		rb.Append("ch1", []byte(string(rune('0'+i))))
	}

	historical := rb.Get("ch1", time.Time{}, 3)
	assert.Equal(t, 3, len(historical))
}

func TestRingBufferPerChannelTTL(t *testing.T) {
	rb := NewRingBuffer(100, time.Hour)

	rb.SetChannelTTL("short", 50*time.Millisecond)
	rb.SetChannelTTL("long", time.Hour)

	rb.Append("short", []byte("short-lived"))
	rb.Append("long", []byte("long-lived"))

	time.Sleep(100 * time.Millisecond)
	rb.evictExpired()

	historicalShort := rb.Get("short", time.Time{}, 0)
	assert.Equal(t, 0, len(historicalShort), "short TTL channel should be evicted")

	historicalLong := rb.Get("long", time.Time{}, 0)
	assert.Equal(t, 1, len(historicalLong), "long TTL channel should still exist")
	assert.Equal(t, "long-lived", string(historicalLong[0]))
}

func TestRingBufferEmptyGet(t *testing.T) {
	rb := NewRingBuffer(10, time.Hour)

	historical := rb.Get("nonexistent", time.Time{}, 0)
	assert.Equal(t, 0, len(historical))
}

func TestBrokerPublishSubscribe(t *testing.T) {
	b, err := New(Config{
		NodeID:     "test-pubsub",
		Port:       4233,
		BufferSize: 100,
	})
	assert.NilError(t, err)
	assert.Assert(t, b != nil)
	defer b.Shutdown()

	historical, live := b.Subscribe("testchannel", time.Time{}, 10)
	assert.Equal(t, 0, len(historical))

	err = b.Publish("testchannel", []byte("hello"))
	assert.NilError(t, err)

	select {
	case data := <-live:
		assert.Equal(t, "hello", string(data))
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for message")
	}

	b.Unsubscribe("testchannel", live)
}

func TestBrokerSubscribeReturnsHistorical(t *testing.T) {
	b, err := New(Config{NodeID: "test-hist", Port: 4234, BufferSize: 100})
	assert.NilError(t, err)
	assert.Assert(t, b != nil)
	defer b.Shutdown()

	err = b.Publish("histchan", []byte("old1"))
	assert.NilError(t, err)
	err = b.Publish("histchan", []byte("old2"))
	assert.NilError(t, err)

	time.Sleep(100 * time.Millisecond)

	historical, live := b.Subscribe("histchan", time.Time{}, 10)
	assert.Equal(t, 2, len(historical))
	assert.Equal(t, "old1", string(historical[0]))
	assert.Equal(t, "old2", string(historical[1]))

	err = b.Publish("histchan", []byte("live"))
	assert.NilError(t, err)

	select {
	case data := <-live:
		assert.Equal(t, "live", string(data))
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for live message")
	}

	b.Unsubscribe("histchan", live)
}

func TestBrokerSubscribeWithSince(t *testing.T) {
	b, err := New(Config{NodeID: "test-since", Port: 4246, BufferSize: 100})
	assert.NilError(t, err)
	assert.Assert(t, b != nil)
	defer b.Shutdown()

	err = b.Publish("sincechan", []byte("before"))
	assert.NilError(t, err)
	time.Sleep(200 * time.Millisecond)

	since := time.Now()
	time.Sleep(50 * time.Millisecond)

	err = b.Publish("sincechan", []byte("after1"))
	assert.NilError(t, err)
	err = b.Publish("sincechan", []byte("after2"))
	assert.NilError(t, err)
	time.Sleep(100 * time.Millisecond)

	historical, live := b.Subscribe("sincechan", since, 10)
	assert.Equal(t, 2, len(historical))
	assert.Equal(t, "after1", string(historical[0]))
	assert.Equal(t, "after2", string(historical[1]))
	b.Unsubscribe("sincechan", live)
}

func TestRingBufferPerChannelTTLEvictionMixed(t *testing.T) {
	rb := NewRingBuffer(100, time.Hour)

	rb.SetChannelTTL("short", 50*time.Millisecond)

	rb.Append("short", []byte("short-lived"))
	rb.Append("long", []byte("long-lived"))

	time.Sleep(100 * time.Millisecond)
	rb.evictExpired()

	historicalShort := rb.Get("short", time.Time{}, 0)
	assert.Equal(t, 0, len(historicalShort), "short TTL channel should be evicted")

	historicalLong := rb.Get("long", time.Time{}, 0)
	assert.Equal(t, 1, len(historicalLong), "long TTL channel should still exist")
	assert.Equal(t, "long-lived", string(historicalLong[0]))
}
