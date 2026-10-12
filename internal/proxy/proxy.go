package proxy

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/webcenter-fr/gohookbridge/pkg/crypto"
	"github.com/webcenter-fr/gohookbridge/pkg/urlutil"

	"github.com/urfave/cli/v2"
)

func startProxy(c *cli.Context) error {
	var pubKeyBytes *[32]byte
	if pubKeyStr := c.String("pubkey"); pubKeyStr != "" {
		var err error
		pubKeyBytes, err = crypto.ParsePublicKey(pubKeyStr)
		if err != nil {
			return fmt.Errorf("invalid public key: %w", err)
		}
	} else if pubKeyFile := c.String("pubkey-file"); pubKeyFile != "" {
		pub, _, err := crypto.LoadKeyPair(pubKeyFile)
		if err != nil {
			return fmt.Errorf("load key file: %w", err)
		}
		pubKeyBytes = pub
	} else {
		return fmt.Errorf("public key required: use --pubkey or --pubkey-file")
	}

	listenAddr := c.String("listen")
	targetURL := c.String("target")
	if targetURL == "" {
		return fmt.Errorf("target URL required: use --target")
	}

	if token := c.String("token"); token != "" {
		targetURL = urlutil.URLWithQueryParam(targetURL, "token", token)
	}

	insecureSkip := c.Bool("insecure-skip-tls-verify")

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecureSkip}, //nolint:gosec // user-configured
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	client := &http.Client{Transport: tr, Timeout: 60 * time.Second}
	maxBodySize := c.Int("max-body-size")

	mux := http.NewServeMux()
	mux.HandleFunc("/", proxyHandler(pubKeyBytes, targetURL, client, maxBodySize))

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
	}

	fmt.Fprintf(os.Stdout, "Encrypt proxy listening on %s, forwarding to %s\n", listenAddr, redactedTargetURL(targetURL))
	return srv.ListenAndServe()
}

// proxyHandler returns the encrypting-proxy request handler: it bounds and
// reads the inbound body (F11), encrypts it for the channel, and forwards it
// to the target server with sanitized headers (F10).
func proxyHandler(pubKeyBytes *[32]byte, targetURL string, client *http.Client, maxBodySize int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
			return
		}

		// F11: bound the inbound body before reading so an oversized or slow
		// producer cannot exhaust proxy memory.
		r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodySize))
		body, err := io.ReadAll(r.Body)
		if err != nil {
			if strings.Contains(err.Error(), "http: request body too large") {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		r.Body.Close()

		encrypted, err := crypto.Encrypt(body, pubKeyBytes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: encrypt error: %v\n", err)
			http.Error(w, "encryption failed", http.StatusInternalServerError)
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(encrypted))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// F10: sanitize headers at the proxy trust boundary (see
		// forwardableProxyHeaders). Hop-by-hop headers are dropped by the
		// allowlist; Content-Length is set from the encrypted body below.
		req.Header = forwardableProxyHeaders(r)
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = int64(len(encrypted))

		resp, err := client.Do(req) //nolint:gosec // user-configured URL
		if err != nil {
			http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

// proxyForwardAllowlist is the fixed set of inbound headers the encrypting
// proxy forwards to the gohookbridge server (F10). It mirrors the server-side
// webhook allowlist (internal/handler/webhook.go forwardedHeaderAllowlist)
// plus the GitLab ingestion token, which the server consumes to authenticate
// ingestion but never relays. Credentials (Authorization, Cookie, ...),
// proxy/identity/routing headers (X-Real-IP, X-Forwarded-Host, ...) and
// arbitrary custom headers are dropped at this trust boundary.
var proxyForwardAllowlist = map[string]struct{}{
	"content-type":         {},
	"user-agent":           {},
	"x-event-type":         {},
	"x-github-event":       {},
	"x-github-delivery":    {},
	"x-github-hook-id":     {},
	"x-gitlab-event":       {},
	"x-gitlab-event-uuid":  {},
	"x-gitlab-token":       {},
	"x-gitea-event":        {},
	"x-gitea-delivery":     {},
	"x-gitea-signature":    {},
	"x-event-key":          {},
	"x-bitbucket-event":    {},
	"x-bitbucket-delivery": {},
	"x-hub-signature":      {},
	"x-hub-signature-256":  {},
}

// forwardableProxyHeaders builds the upstream header set from an inbound
// request: allowlisted provider metadata and ingestion/signature headers only,
// with X-Forwarded-For replaced by the immediate client IP so the server's
// trusted-proxy handling (F18) can never be fed a forged identity by a public
// producer (F10).
func forwardableProxyHeaders(r *http.Request) http.Header {
	fwd := make(http.Header, len(r.Header))
	for k, vals := range r.Header {
		if _, ok := proxyForwardAllowlist[strings.ToLower(k)]; !ok || len(vals) == 0 {
			continue
		}
		fwd[k] = vals
	}
	clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		clientIP = r.RemoteAddr
	}
	fwd.Set("X-Forwarded-For", clientIP)
	return fwd
}

// redactedTargetURL returns the target URL with credential query parameters
// (the channel access token) replaced, so the startup log never leaks
// secrets (CWE-532, F33).
func redactedTargetURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid-url>"
	}
	q := parsed.Query()
	if q.Get("token") != "" {
		q.Set("token", "<redacted>")
		parsed.RawQuery = q.Encode()
	}
	return parsed.String()
}
