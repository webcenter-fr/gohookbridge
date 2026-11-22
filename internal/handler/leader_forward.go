package handler

import (
	"net/http"

	"github.com/disaster37/go-raft/httpext"
	"github.com/disaster37/go-raft/transport"
)

const leaderForwardedHeader = "X-Gohookbridge-Leader-Forwarded"

// StripForwardMarker removes any client-supplied leader-forward marker from
// inbound requests via go-raft/httpext (F13).
func StripForwardMarker(next http.Handler) http.Handler {
	return httpext.StripForwardMarker(leaderForwardedHeader)(next)
}

// LeaderForwardMiddleware forwards mutating API requests from followers to the
// Raft leader over a pooled, hot-reloading mTLS client via go-raft/httpext (F13).
func LeaderForwardMiddleware(rs httpext.LeaderInfo, httpPort int, tlsProvider *transport.DynamicTLSProvider, scheme string) func(http.Handler) http.Handler {
	if scheme == "" {
		scheme = "https"
	}
	return httpext.LeaderForwardMiddleware(httpext.LeaderForwardConfig{
		LeaderInfo:  rs,
		TargetPort:  httpPort,
		Scheme:      scheme,
		HeaderName:  leaderForwardedHeader,
		TLSProvider: tlsProvider,
	})
}
