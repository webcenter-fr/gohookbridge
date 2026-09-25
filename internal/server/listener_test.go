package server

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestResolveListenerConfig(t *testing.T) {
	tests := []struct {
		name          string
		address       string
		port          int
		publicAddress string
		publicPort    int
		wantInternal  string
		wantPublic    string
		wantErr       string
	}{
		{
			name:          "public disabled",
			address:       "localhost",
			port:          8081,
			publicAddress: "0.0.0.0",
			publicPort:    0,
			wantInternal:  "localhost:8081",
			wantPublic:    "",
		},
		{
			name:          "public enabled",
			address:       "127.0.0.1",
			port:          8081,
			publicAddress: "0.0.0.0",
			publicPort:    8082,
			wantInternal:  "127.0.0.1:8081",
			wantPublic:    "0.0.0.0:8082",
		},
		{
			name:          "same port rejected",
			address:       "localhost",
			port:          8081,
			publicAddress: "0.0.0.0",
			publicPort:    8081,
			wantErr:       "must differ from port",
		},
		{
			name:          "negative public port rejected",
			address:       "localhost",
			port:          8081,
			publicAddress: "0.0.0.0",
			publicPort:    -1,
			wantErr:       "public-port must be >= 0",
		},
		{
			name:          "non-positive port rejected",
			address:       "localhost",
			port:          0,
			publicAddress: "0.0.0.0",
			publicPort:    0,
			wantErr:       "port must be greater than 0",
		},
		{
			name:          "ipv6 addresses bracketed by JoinHostPort",
			address:       "::1",
			port:          8081,
			publicAddress: "::",
			publicPort:    8082,
			wantInternal:  "[::1]:8081",
			wantPublic:    "[::]:8082",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := resolveListenerConfig(tt.address, tt.port, tt.publicAddress, tt.publicPort)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, cfg.internalAddr, tt.wantInternal)
			assert.Equal(t, cfg.publicAddr, tt.wantPublic)
		})
	}
}

func TestEffectivePublicAddr(t *testing.T) {
	assert.Equal(t, effectivePublicAddr("localhost", 8081), "localhost:8081")
	assert.Equal(t, effectivePublicAddr("127.0.0.1", 8081), "127.0.0.1:8081")
	assert.Equal(t, effectivePublicAddr("::1", 8081), "[::1]:8081")
	assert.Equal(t, effectivePublicAddr("", 8081), ":8081")
}
