package handler

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
)

// leaderForwardedHeader marks a request that was already forwarded to the Raft
// leader. It prevents a forwarding loop when leadership changes while the
// request is in flight.
const leaderForwardedHeader = "X-Gohookbridge-Leader-Forwarded"

// leaderInfo is the subset of the Raft store the leader forwarding middleware
// needs. *store.RaftStore implements it.
type leaderInfo interface {
	IsLeader() bool
	LeaderAddress() string
}

// StripForwardMarker removes any client-supplied leader-forward marker from an
// inbound request so only the trusted forwarding path can set it.
func StripForwardMarker(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(leaderForwardedHeader)
		next.ServeHTTP(w, r)
	})
}

// LeaderForwardMiddleware forwards mutating API requests to the Raft leader
// over an authenticated TLS transport. The leader address is the Raft
// transport address, so only its host is kept and the API port is substituted.
func LeaderForwardMiddleware(rs leaderInfo, httpPort int, transport *http.Transport, scheme string) func(http.Handler) http.Handler {
	if scheme == "" {
		scheme = "https"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isReadOnlyMethod(r.Method) || rs.IsLeader() || r.Header.Get(leaderForwardedHeader) != "" {
				next.ServeHTTP(w, r)
				return
			}

			leaderAddr := rs.LeaderAddress()
			host, _, err := net.SplitHostPort(leaderAddr)
			if leaderAddr == "" || err != nil {
				http.Error(w, `{"error":"no Raft leader available"}`, http.StatusServiceUnavailable)
				return
			}

			target := &url.URL{
				Scheme: scheme,
				Host:   net.JoinHostPort(host, strconv.Itoa(httpPort)),
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			if transport != nil {
				proxy.Transport = transport
			}
			proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, proxyErr error) {
				message := fmt.Sprintf(`{"error":%q}`, "forward to Raft leader failed: "+proxyErr.Error())
				http.Error(w, message, http.StatusBadGateway)
			}
			r.Header.Set(leaderForwardedHeader, "1")
			proxy.ServeHTTP(w, r)
		})
	}
}

func isReadOnlyMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
