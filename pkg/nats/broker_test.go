package nats

import (
	"crypto/tls"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestRingBufferAppendAndGet(t *testing.T) {
	rb := NewRingBuffer(5, 0, time.Hour)

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
	rb := NewRingBuffer(3, 0, time.Hour)

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
	rb := NewRingBuffer(100, 0, 50*time.Millisecond)

	rb.Append("ch1", []byte("data1"))
	time.Sleep(100 * time.Millisecond)
	rb.evictExpired()

	historical := rb.Get("ch1", time.Time{}, 0)
	assert.Equal(t, 0, len(historical))
}

func TestRingBufferGetLimit(t *testing.T) {
	rb := NewRingBuffer(100, 0, time.Hour)

	for i := 0; i < 10; i++ {
		rb.Append("ch1", []byte(string(rune('0'+i))))
	}

	historical := rb.Get("ch1", time.Time{}, 3)
	assert.Equal(t, 3, len(historical))
}

func TestRingBufferPerChannelTTL(t *testing.T) {
	rb := NewRingBuffer(100, 0, time.Hour)

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
	rb := NewRingBuffer(10, 0, time.Hour)

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

func TestBrokerFanoutMultipleSubscribers(t *testing.T) {
	b, err := New(Config{
		NodeID:     "test-fanout",
		Port:       4235,
		BufferSize: 100,
	})
	assert.NilError(t, err)
	defer b.Shutdown()

	_, live1 := b.Subscribe("fanout", time.Time{}, 10)
	_, live2 := b.Subscribe("fanout", time.Time{}, 10)
	defer b.Unsubscribe("fanout", live1)
	defer b.Unsubscribe("fanout", live2)

	err = b.Publish("fanout", []byte("fanout-test"))
	assert.NilError(t, err)

	select {
	case data := <-live1:
		assert.Equal(t, "fanout-test", string(data))
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for subscriber 1")
	}

	select {
	case data := <-live2:
		assert.Equal(t, "fanout-test", string(data))
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for subscriber 2")
	}
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

func TestBrokerClusterFanout(t *testing.T) {
	brokerA, err := New(Config{
		NodeID:      "cluster-a",
		Port:        4261,
		ClusterPort: 6261,
		BufferSize:  100,
		ClusterName: "test-cluster",
	})
	assert.NilError(t, err)
	defer brokerA.Shutdown()

	brokerB, err := New(Config{
		NodeID:      "cluster-b",
		Port:        4262,
		ClusterPort: 6262,
		Routes:      []string{"nats://127.0.0.1:6261"},
		BufferSize:  100,
		ClusterName: "test-cluster",
	})
	assert.NilError(t, err)
	defer brokerB.Shutdown()

	_, live := brokerB.Subscribe("fanout", time.Time{}, 10)
	defer brokerB.Unsubscribe("fanout", live)

	// The route between the embedded servers may take a moment to connect,
	// so retry the publish until the subscriber on broker B sees it.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		assert.NilError(t, brokerA.Publish("fanout", []byte("ping")))
		select {
		case data := <-live:
			assert.Equal(t, "ping", string(data))
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
	t.Fatal("timeout waiting for cross-node NATS fan-out")
}

func TestRingBufferPerChannelTTLEvictionMixed(t *testing.T) {
	rb := NewRingBuffer(100, 0, time.Hour)

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

func TestClusterTLSAndAuthConfigured(t *testing.T) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	opts, err := buildServerOptions(&Config{
		NodeID:          "n1",
		Port:            4222,
		ClusterPort:     6222,
		Routes:          []string{"nats://nats-0.nats:6222"},
		ClusterName:     "gohookbridge",
		ClusterUsername: "route-user",
		ClusterPassword: "route-pass",
		ClusterTLS:      tlsCfg,
	})
	assert.NilError(t, err)
	assert.Equal(t, opts.Cluster.Username, "route-user")
	assert.Equal(t, opts.Cluster.Password, "route-pass")
	assert.Equal(t, opts.Cluster.TLSConfig, tlsCfg)
	assert.Assert(t, opts.Cluster.TLSTimeout > 0)
	assert.Equal(t, 1, len(opts.Routes))
	assert.Equal(t, "route-user", opts.Routes[0].User.Username())
	pass, _ := opts.Routes[0].User.Password()
	assert.Equal(t, "route-pass", pass)
}

func TestDisableClusterSingleNode(t *testing.T) {
	opts, err := buildServerOptions(&Config{
		NodeID:         "n1",
		Port:           4222,
		ClusterPort:    6222,
		Routes:         []string{"nats://nats-0.nats:6222"},
		DisableCluster: true,
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, opts.Cluster.Port, "cluster listener must be disabled")
	assert.Equal(t, 0, len(opts.Routes), "routes must be dropped when the cluster is disabled")
}
