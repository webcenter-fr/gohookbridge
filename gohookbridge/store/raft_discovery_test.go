package store

import (
	"fmt"
	"net"
	"slices"
	"testing"

	"gotest.tools/v3/assert"
)

func TestNewPeerResolverSelection(t *testing.T) {
	tests := []struct {
		name string
		cfg  RaftDiscoveryConfig
		want string
	}{
		{
			name: "explicit peers wins",
			cfg:  RaftDiscoveryConfig{Peers: []RaftPeer{{ID: "a", Address: "a:1"}}, StatefulSetName: "sts", HeadlessService: "h"},
			want: "*store.staticPeerResolver",
		},
		{
			name: "dns discovery",
			cfg:  RaftDiscoveryConfig{StatefulSetName: "sts", HeadlessService: "h", Namespace: "ns", Replicas: 3},
			want: "*store.dnsPeerResolver",
		},
		{
			name: "single node fallback",
			cfg:  RaftDiscoveryConfig{BindAddr: "127.0.0.1:6001"},
			want: "*store.singleNodeResolver",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, fmt.Sprintf("%T", NewPeerResolver(&tc.cfg)), tc.want)
		})
	}
}

func TestDNSResolver_Resolve(t *testing.T) {
	tests := []struct {
		name    string
		cfg     RaftDiscoveryConfig
		want    []RaftPeer
		wantErr bool
	}{
		{
			name: "three replicas with cluster domain",
			cfg: RaftDiscoveryConfig{
				StatefulSetName: "gohookbridge-server",
				HeadlessService: "gohookbridge-server-headless",
				Namespace:       "gohookbridge",
				ClusterDomain:   "cluster.local",
				Replicas:        3,
				RaftPort:        6001,
			},
			want: []RaftPeer{
				{ID: "gohookbridge-server-0", Address: "gohookbridge-server-0.gohookbridge-server-headless.gohookbridge.svc.cluster.local:6001"},
				{ID: "gohookbridge-server-1", Address: "gohookbridge-server-1.gohookbridge-server-headless.gohookbridge.svc.cluster.local:6001"},
				{ID: "gohookbridge-server-2", Address: "gohookbridge-server-2.gohookbridge-server-headless.gohookbridge.svc.cluster.local:6001"},
			},
		},
		{
			name: "single replica",
			cfg: RaftDiscoveryConfig{
				StatefulSetName: "sts",
				HeadlessService: "headless",
				Namespace:       "ns",
				ClusterDomain:   "cluster.local",
				Replicas:        1,
				RaftPort:        6001,
			},
			want: []RaftPeer{{ID: "sts-0", Address: "sts-0.headless.ns.svc.cluster.local:6001"}},
		},
		{
			name: "custom cluster domain",
			cfg: RaftDiscoveryConfig{
				StatefulSetName: "sts",
				HeadlessService: "headless",
				Namespace:       "ns",
				ClusterDomain:   "cluster.internal",
				Replicas:        1,
				RaftPort:        6001,
			},
			want: []RaftPeer{{ID: "sts-0", Address: "sts-0.headless.ns.svc.cluster.internal:6001"}},
		},
		{
			name:    "zero replicas",
			cfg:     RaftDiscoveryConfig{StatefulSetName: "sts", HeadlessService: "h", Namespace: "ns", Replicas: 0},
			wantErr: true,
		},
		{
			name:    "missing statefulset name",
			cfg:     RaftDiscoveryConfig{HeadlessService: "h", Namespace: "ns", Replicas: 3},
			wantErr: true,
		},
		{
			name:    "missing headless service",
			cfg:     RaftDiscoveryConfig{StatefulSetName: "sts", Namespace: "ns", Replicas: 3},
			wantErr: true,
		},
		{
			name:    "missing namespace",
			cfg:     RaftDiscoveryConfig{StatefulSetName: "sts", HeadlessService: "h", Replicas: 3},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &dnsPeerResolver{cfg: tc.cfg, clusterDomain: clusterDomain(&tc.cfg)}
			got, err := r.Resolve()
			if tc.wantErr {
				assert.Assert(t, err != nil, "expected error")
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tc.want)
		})
	}
}

func TestDNSResolver_Resolve_NoClusterDomain(t *testing.T) {
	cfg := RaftDiscoveryConfig{
		StatefulSetName: "sts",
		HeadlessService: "headless",
		Namespace:       "ns",
		ClusterDomain:   "",
		Replicas:        2,
		RaftPort:        6001,
	}
	r := &dnsPeerResolver{cfg: cfg, clusterDomain: clusterDomain(&cfg)}
	got, err := r.Resolve()
	assert.NilError(t, err)
	assert.DeepEqual(t, got, []RaftPeer{
		{ID: "sts-0", Address: "sts-0.headless.ns.svc:6001"},
		{ID: "sts-1", Address: "sts-1.headless.ns.svc:6001"},
	})
}

func TestDNSResolver_Self(t *testing.T) {
	cfg := RaftDiscoveryConfig{
		StatefulSetName: "sts",
		HeadlessService: "headless",
		Namespace:       "ns",
		ClusterDomain:   "cluster.local",
		Replicas:        3,
		RaftPort:        6001,
	}
	r := &dnsPeerResolver{cfg: cfg, clusterDomain: clusterDomain(&cfg)}

	t.Run("matches pod ordinal", func(t *testing.T) {
		self, err := r.selfFor("sts-1")
		assert.NilError(t, err)
		assert.DeepEqual(t, self, RaftPeer{ID: "sts-1", Address: "sts-1.headless.ns.svc.cluster.local:6001"})
	})

	t.Run("unknown hostname with advertise", func(t *testing.T) {
		r2 := &dnsPeerResolver{cfg: RaftDiscoveryConfig{
			StatefulSetName: "sts",
			HeadlessService: "headless",
			Namespace:       "ns",
			NodeID:          "custom",
			AdvertiseAddr:   "custom.example:6001",
		}, clusterDomain: "cluster.local"}
		self, err := r2.selfFor("other-host")
		assert.NilError(t, err)
		assert.DeepEqual(t, self, RaftPeer{ID: "custom", Address: "custom.example:6001"})
	})

	t.Run("unknown hostname without advertise errors", func(t *testing.T) {
		_, err := r.selfFor("other-host")
		assert.Assert(t, err != nil)
	})

	t.Run("Self resolves via injected hostname", func(t *testing.T) {
		r3 := &dnsPeerResolver{cfg: cfg, clusterDomain: clusterDomain(&cfg), hostname: "sts-1"}
		self, err := r3.Self()
		assert.NilError(t, err)
		assert.DeepEqual(t, self, RaftPeer{ID: "sts-1", Address: "sts-1.headless.ns.svc.cluster.local:6001"})
	})
}

func TestPodOrdinal(t *testing.T) {
	tests := []struct {
		host string
		sts  string
		n    int
		ok   bool
	}{
		{"sts-0", "sts", 0, true},
		{"sts-12", "sts", 12, true},
		{"sts-x", "sts", 0, false},
		{"sts-", "sts", 0, false},
		{"other-1", "sts", 0, false},
		{"sts-1", "", 0, false},
		{"sts--1", "sts", 0, false},
	}
	for _, tc := range tests {
		n, ok := podOrdinal(tc.host, tc.sts)
		assert.Equal(t, n, tc.n, "podOrdinal(%q,%q) ordinal", tc.host, tc.sts)
		assert.Equal(t, ok, tc.ok, "podOrdinal(%q,%q) ok", tc.host, tc.sts)
	}
}

func TestStaticResolver_MatchSelf(t *testing.T) {
	peers := []RaftPeer{
		{ID: "node-1", Address: "node-1.example:6001"},
		{ID: "node-2", Address: "node-2.example:6001"},
	}

	t.Run("by node id", func(t *testing.T) {
		r := &staticPeerResolver{cfg: RaftDiscoveryConfig{Peers: peers, NodeID: "node-2"}}
		self, err := r.Self()
		assert.NilError(t, err)
		assert.DeepEqual(t, self, peers[1])
	})

	t.Run("by advertise addr", func(t *testing.T) {
		r := &staticPeerResolver{cfg: RaftDiscoveryConfig{Peers: peers, AdvertiseAddr: "node-1.example:6001"}}
		self, err := r.Self()
		assert.NilError(t, err)
		assert.DeepEqual(t, self, peers[0])
	})

	t.Run("not found errors", func(t *testing.T) {
		r := &staticPeerResolver{cfg: RaftDiscoveryConfig{Peers: peers, NodeID: "node-3"}}
		_, err := r.matchSelf()
		assert.Assert(t, err != nil)
	})
}

func TestStaticResolver_SynthesizeSelf(t *testing.T) {
	peers := []RaftPeer{
		{ID: "node-1", Address: "node-1.example:6001"},
		{ID: "node-2", Address: "node-2.example:6001"},
	}

	t.Run("prepends synthesized self", func(t *testing.T) {
		r := &staticPeerResolver{cfg: RaftDiscoveryConfig{Peers: peers, NodeID: "node-3", AdvertiseAddr: "node-3.example:6001"}}
		resolved, err := r.Resolve()
		assert.NilError(t, err)
		assert.Equal(t, len(resolved), 3)
		assert.DeepEqual(t, resolved[0], RaftPeer{ID: "node-3", Address: "node-3.example:6001"})
	})

	t.Run("falls back to bind host", func(t *testing.T) {
		r := &staticPeerResolver{cfg: RaftDiscoveryConfig{Peers: peers, NodeID: "node-3", BindAddr: "10.0.0.5:6001"}}
		self, err := r.Self()
		assert.NilError(t, err)
		assert.DeepEqual(t, self, RaftPeer{ID: "node-3", Address: "10.0.0.5:6001"})
	})

	t.Run("cannot identify self", func(t *testing.T) {
		r := &staticPeerResolver{cfg: RaftDiscoveryConfig{Peers: peers}}
		_, err := r.Self()
		assert.Assert(t, err != nil)
	})
}

func TestSingleNodeResolver(t *testing.T) {
	r := &singleNodeResolver{cfg: RaftDiscoveryConfig{BindAddr: "127.0.0.1:6001", NodeID: "solo"}}
	peers, err := r.Resolve()
	assert.NilError(t, err)
	assert.DeepEqual(t, peers, []RaftPeer{{ID: "solo", Address: "127.0.0.1:6001"}})
}

func TestDeriveAdvertiseAddr(t *testing.T) {
	tests := []struct {
		name     string
		cfg      RaftDiscoveryConfig
		hostname string
		want     string
	}{
		{
			name:     "explicit advertise",
			cfg:      RaftDiscoveryConfig{AdvertiseAddr: "explicit.example:9000"},
			hostname: "sts-2",
			want:     "explicit.example:9000",
		},
		{
			name: "pod fqdn derivation",
			cfg: RaftDiscoveryConfig{
				StatefulSetName: "sts",
				HeadlessService: "headless",
				Namespace:       "ns",
				ClusterDomain:   "cluster.local",
				RaftPort:        6001,
			},
			hostname: "sts-2",
			want:     "sts-2.headless.ns.svc.cluster.local:6001",
		},
		{
			name: "pod svc-only derivation",
			cfg: RaftDiscoveryConfig{
				StatefulSetName: "sts",
				HeadlessService: "headless",
				Namespace:       "ns",
				ClusterDomain:   "",
				RaftPort:        6001,
			},
			hostname: "sts-2",
			want:     "sts-2.headless.ns.svc:6001",
		},
		{
			name: "hostname not matching falls back to bind",
			cfg: RaftDiscoveryConfig{
				StatefulSetName: "sts",
				HeadlessService: "headless",
				Namespace:       "ns",
				BindAddr:        "10.0.0.5:6001",
			},
			hostname: "other-host",
			want:     "10.0.0.5:6001",
		},
		{
			name:     "wildcard bind falls back to loopback",
			cfg:      RaftDiscoveryConfig{BindAddr: "0.0.0.0:6001"},
			hostname: "other-host",
			want:     "127.0.0.1:6001",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DeriveAdvertiseAddr(&tc.cfg, tc.hostname)
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
		})
	}
}

func TestDeriveBindHost(t *testing.T) {
	tests := []struct {
		name     string
		bindAddr string
		port     int
		want     string
	}{
		{"concrete host", "10.0.0.5:6001", 6001, "10.0.0.5:6001"},
		{"wildcard ipv4", "0.0.0.0:6001", 6001, "127.0.0.1:6001"},
		{"wildcard ipv6", "[::]:6001", 6001, "127.0.0.1:6001"},
		{"empty host", ":6001", 6001, "127.0.0.1:6001"},
		{"unparseable falls back to port", "not-an-addr", 6001, "127.0.0.1:6001"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, deriveBindHost(tc.bindAddr, tc.port), tc.want)
		})
	}
}

func TestPodSANs(t *testing.T) {
	tests := []struct {
		name     string
		cfg      RaftDiscoveryConfig
		hostname string
		wantDNS  []string
		wantFQDN string
	}{
		{
			name: "fqdn san present, loopback ip only",
			cfg: RaftDiscoveryConfig{
				HeadlessService: "headless",
				Namespace:       "ns",
				ClusterDomain:   "cluster.local",
			},
			hostname: "sts-0",
			wantDNS: []string{
				"sts-0",
				"localhost",
				"sts-0.headless",
				"sts-0.headless.ns",
				"sts-0.headless.ns.svc",
				"sts-0.headless.ns.svc.cluster.local",
			},
			wantFQDN: "sts-0.headless.ns.svc.cluster.local",
		},
		{
			name:     "no headless service only hostname and localhost",
			cfg:      RaftDiscoveryConfig{},
			hostname: "plain-host",
			wantDNS:  []string{"plain-host", "localhost"},
		},
		{
			name: "empty cluster domain sans end at svc",
			cfg: RaftDiscoveryConfig{
				HeadlessService: "headless",
				Namespace:       "ns",
				ClusterDomain:   "",
			},
			hostname: "sts-0",
			wantDNS: []string{
				"sts-0",
				"localhost",
				"sts-0.headless",
				"sts-0.headless.ns",
				"sts-0.headless.ns.svc",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dns, ips := PodSANs(&tc.cfg, tc.hostname)
			assert.DeepEqual(t, dns, tc.wantDNS)
			if tc.wantFQDN != "" {
				assert.Assert(t, slices.Contains(dns, tc.wantFQDN), "DNS SANs must contain %q", tc.wantFQDN)
			}
			// Discovery is FQDN-only: the IP SAN set is exactly [127.0.0.1].
			assert.Equal(t, len(ips), 1)
			assert.Assert(t, ips[0].Equal(net.ParseIP("127.0.0.1")))
		})
	}
}
