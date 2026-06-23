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

	gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"

	"github.com/urfave/cli/v2"
)

func startProxy(c *cli.Context) error {
	var pubKeyBytes *[32]byte
	if pubKeyStr := c.String("pubkey"); pubKeyStr != "" {
		var err error
		pubKeyBytes, err = gohookbridge.ParsePublicKey(pubKeyStr)
		if err != nil {
			return fmt.Errorf("invalid public key: %w", err)
		}
	} else if pubKeyFile := c.String("pubkey-file"); pubKeyFile != "" {
		pub, _, err := gohookbridge.LoadKeyPair(pubKeyFile)
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
		targetURL = gohookbridge.URLWithQueryParam(targetURL, "token", token)
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
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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

		encrypted, err := gohookbridge.Encrypt(body, pubKeyBytes)
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
		req.Header.Set("Content-Type", "application/json")
		req.Header.Del("Content-Length")
		req.Header.Del("Transfer-Encoding")
		req.Header.Del("Connection")
		req.Header.Del("Keep-Alive")
		req.Header.Del("Proxy-Authorization")
		req.Header.Del("TE")
		req.Header.Del("Trailer")
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
	})

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
	}

	fmt.Fprintf(os.Stdout, "Encrypt proxy listening on %s, forwarding to %s\n", listenAddr, targetURL)
	return srv.ListenAndServe()
}
