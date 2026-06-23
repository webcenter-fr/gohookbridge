package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"

	"github.com/urfave/cli/v2"
)

func produce(c *cli.Context) error {
	serverURL := c.Args().First()
	if serverURL == "" {
		return fmt.Errorf("server URL required: gohookbridge produce --pubkey <key> <server-url>/<channel> [payload-file]")
	}

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

	var payload []byte
	var err error
	if fileArg := c.Args().Get(1); fileArg != "" {
		payload, err = os.ReadFile(fileArg)
		if err != nil {
			return fmt.Errorf("read payload file: %w", err)
		}
	} else {
		payload, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
	}

	if len(payload) == 0 {
		return fmt.Errorf("empty payload")
	}

	encrypted, err := gohookbridge.Encrypt(payload, pubKeyBytes)
	if err != nil {
		return fmt.Errorf("encrypt payload: %w", err)
	}

	if token := c.String("token"); token != "" {
		serverURL = gohookbridge.URLWithQueryParam(serverURL, "token", token)
	}

	resp, err := postEncryptedPayload(serverURL, encrypted, c.Bool("insecure-skip-tls-verify"))
	if err != nil {
		return fmt.Errorf("post encrypted payload: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Fprintf(os.Stdout, "%s\n", body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return nil
}

func postEncryptedPayload(serverURL string, encrypted []byte, insecureSkipVerify bool) (*http.Response, error) {
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("parse server URL: %w", err)
	}

	if parsedURL.Scheme == "" {
		parsedURL.Scheme = "https"
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecureSkipVerify}, //nolint:gosec // user-configured
	}
	client := &http.Client{Transport: tr}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, parsedURL.String(), bytes.NewReader(encrypted))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	return client.Do(req) //nolint:gosec // user-configured URL
}
