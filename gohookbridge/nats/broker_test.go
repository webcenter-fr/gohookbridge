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

func TestRingBufferEmptyGet(t *testing.T) {
	rb := NewRingBuffer(10, time.Hour)

	historical := rb.Get("nonexistent", time.Time{}, 0)
	assert.Equal(t, 0, len(historical))
}

func TestBrokerStartShutdown(t *testing.T) {
	b, err := New(Config{
		NodeID:     "test-node",
		Port:       0,
		BufferTTL:  time.Hour,
		BufferSize: 100,
	})
	assert.NilError(t, err)
	assert.Assert(t, b == nil)
}

func TestBrokerPublishSubscribe(t *testing.T) {
	b, err := New(Config{
		NodeID:     "test-pubsub",
		Port:       4233,
		BufferTTL:  time.Hour,
		BufferSize: 100,
	})
	assert.NilError(t, err)
	assert.Assert(t, b != nil)
	defer b.Shutdown()

	historical, live := b.Subscribe("testchannel", 10)
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
	b, err := New(Config{
		NodeID:     "test-hist",
		Port:       4234,
		BufferTTL:  time.Hour,
		BufferSize: 100,
	})
	assert.NilError(t, err)
	assert.Assert(t, b != nil)
	defer b.Shutdown()

	err = b.Publish("histchan", []byte("old1"))
	assert.NilError(t, err)
	err = b.Publish("histchan", []byte("old2"))
	assert.NilError(t, err)

	time.Sleep(100 * time.Millisecond)

	historical, live := b.Subscribe("histchan", 10)
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