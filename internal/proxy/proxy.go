package proxy

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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

	mux := http.NewServeMux()
	mux.Handle("/", newProxyHandler(pubKeyBytes, targetURL, client))

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
		// ReadTimeout bounds slow-body/slow-header reads; IdleTimeout bounds keep-alive.
		// No WriteTimeout: responses are small JSON replies, streamed as received.
		ReadTimeout: 60 * time.Second,
		IdleTimeout: 120 * time.Second,
	}

	fmt.Fprintf(os.Stdout, "Encrypt proxy listening on %s, forwarding to %s\n", listenAddr, targetURL)
	return srv.ListenAndServe()
}

// newProxyHandler builds the POST-only forwarding handler: read the body,
// encrypt it, sanitize the forwarded headers and POST to the target.
func newProxyHandler(pubKeyBytes *[32]byte, targetURL string, client *http.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
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

		req.Header = r.Header.Clone()
		sanitizeForwardedHeaders(req, r.RemoteAddr)
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = int64(len(encrypted))

		resp, err := client.Do(req) //nolint:gosec // user-configured URL
		if err != nil {
			http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		copyResponse(w, resp)
	})
}

// sanitizeForwardedHeaders strips connection-state, hop-by-hop, and
// client-identity headers and sets X-Forwarded-For to the real client (never
// trusts a client-supplied value). Webhook signature headers (X-Hub-Signature*,
// X-Gitlab-Token, X-Gitea-Signature) are intentionally preserved so downstream
// signature validation keeps working.
func sanitizeForwardedHeaders(req *http.Request, remoteAddr string) {
	for _, h := range []string{
		"Content-Length", "Transfer-Encoding", "Connection", "Keep-Alive",
		"Proxy-Authorization", "TE", "Trailer", "Upgrade",
		"Cookie", "Authorization",
		"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP", "Forwarded",
	} {
		req.Header.Del(h)
	}
	if clientIP, _, err := net.SplitHostPort(remoteAddr); err == nil {
		req.Header.Set("X-Forwarded-For", clientIP)
	}
}

// copyResponse mirrors the upstream response headers and body to the caller.
func copyResponse(w http.ResponseWriter, resp *http.Response) {
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
