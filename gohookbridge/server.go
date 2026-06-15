package gohookbridge

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/webcenter-fr/gohookbridge/gohookbridge/nats"
	"github.com/webcenter-fr/gohookbridge/gohookbridge/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/urfave/cli/v2"
	"golang.org/x/crypto/acme/autocert"
)

const (
	timeFormat        = "2006-01-02T15.04.01.000"
	contentType       = "application/json"
	versionHeaderName = "X-Gosmee-Version"
	maxChannelLength  = 64
	channelIDPattern  = "[a-zA-Z0-9_-]{1,64}"
	channelPath       = "/{channel:" + channelIDPattern + "}"
	eventsPath        = "/events/{channel:" + channelIDPattern + "}"
)

var (
	defaultServerPort    = 8081
	defaultServerAddress = "localhost"
)

//go:embed templates/favicon.svg
var faviconSVG []byte

type Subscriber struct {
	Channel   string
	Events    chan []byte
	PublicKey *[32]byte
}

type EventBroker struct {
	sync.RWMutex
	subscribers map[string][]*Subscriber
}

func NewEventBroker() *EventBroker {
	return &EventBroker{
		subscribers: make(map[string][]*Subscriber),
	}
}

func (eb *EventBroker) Subscribe(channel string, pubKey *[32]byte) *Subscriber {
	eb.Lock()
	defer eb.Unlock()

	subscriber := &Subscriber{
		Channel:   channel,
		Events:    make(chan []byte, 100),
		PublicKey: pubKey,
	}

	eb.subscribers[channel] = append(eb.subscribers[channel], subscriber)
	return subscriber
}

func (eb *EventBroker) Unsubscribe(channel string, subscriber *Subscriber) {
	eb.Lock()
	defer eb.Unlock()

	subscribers := eb.subscribers[channel]
	for i, s := range subscribers {
		if s == subscriber {
			eb.subscribers[channel] = slices.Delete(subscribers, i, i+1)
			close(subscriber.Events)
			break
		}
	}

	if len(eb.subscribers[channel]) == 0 {
		delete(eb.subscribers, channel)
	}
}

func (eb *EventBroker) Publish(channel string, data []byte) {
	eb.RLock()
	subscribers := append([]*Subscriber(nil), eb.subscribers[channel]...)
	eb.RUnlock()

	for _, s := range subscribers {
		payload := data
		if s.PublicKey != nil {
			encrypted, err := Encrypt(data, s.PublicKey)
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: encryption failed for subscriber on channel %s: %v\n", s.Channel, err)
				continue
			}
			payload = encrypted
		}

		select {
		case s.Events <- payload:
		default:
			fmt.Fprintf(os.Stdout, "WARNING: event dropped for subscriber on channel %s: buffer full\n", s.Channel)
		}
	}
}

func rejectProtectedChannelRequest(w http.ResponseWriter) {
	http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
}


func effectivePublicURL(publicURL, portAddr string, sslEnabled bool) string {
	if publicURL != "" {
		return publicURL
	}

	scheme := "http://"
	if sslEnabled {
		scheme = "https://"
	}

	return fmt.Sprintf("%s%s", scheme, portAddr)
}


func errorIt(w http.ResponseWriter, _ *http.Request, status int, err error) {
	w.WriteHeader(status)
	_, _ = w.Write([]byte(err.Error()))
}

func validateGitHubWebhookSignature(secret string, payload []byte, signatureHeader string) bool {
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}

	signature := strings.TrimPrefix(signatureHeader, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}

func validateBitbucketHMAC(secret string, payload []byte, signatureHeader string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signatureHeader), []byte(expectedMAC))
}

func validateGiteaSignature(secret string, payload []byte, signatureHeader string) bool {
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}

	signature := strings.TrimPrefix(signatureHeader, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}

func validateWebhookSignature(secret string, payload []byte, r *http.Request) bool {
	if secret == "" {
		return true
	}

	if gitlabToken := r.Header.Get("X-Gitlab-Token"); gitlabToken != "" {
		if subtle.ConstantTimeCompare([]byte(gitlabToken), []byte(secret)) == 1 {
			return true
		}
		return false
	}

	if githubSignature := r.Header.Get("X-Hub-Signature-256"); githubSignature != "" {
		fmt.Fprintf(os.Stdout, "Received request %s %s\n", r.Method, r.URL.Path)
		return validateGitHubWebhookSignature(secret, payload, githubSignature)
	}

	if bitbucketSignature := r.Header.Get("X-Hub-Signature"); bitbucketSignature != "" {
		return validateBitbucketHMAC(secret, payload, bitbucketSignature)
	}

	if giteaSignature := r.Header.Get("X-Gitea-Signature"); giteaSignature != "" {
		return validateGiteaSignature(secret, payload, giteaSignature)
	}

	return false
}

func handleWebhookPost(broker *nats.Broker, rs *store.RaftStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		if !strings.Contains(r.Header.Get("Content-Type"), contentType) {
			http.Error(w, "content-type must be application/json", http.StatusBadRequest)
			return
		}
		channel := chi.URLParam(r, "channel")
		defer r.Body.Close()

		chConfig, _ := rs.ResolveChannelConfig(channel)

		maxBodySize := chConfig.MaxBodySize
		if maxBodySize == 0 {
			maxBodySize, _ = rs.ResolveChannelMaxBodySize(channel)
		}
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

		webhookSecret := chConfig.WebhookSecret
		if webhookSecret == "" {
			webhookSecret, _ = rs.ResolveChannelWebhookSecret(channel)
		}
		if webhookSecret != "" {
			if !validateWebhookSignature(webhookSecret, body, r) {
				http.Error(w, "invalid signature", http.StatusUnauthorized)
				return
			}
		}

		var d any
		if err := json.Unmarshal(body, &d); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		encryptionMode := chConfig.EncryptionMode
		encryptionKey := chConfig.EncryptionKey
		if encryptionMode == "" {
			encryptionMode, encryptionKey, _, _ = rs.ResolveChannelEncryption(channel)
		}

		var payloadBytes []byte
		if encryptionMode == "server_side" && encryptionKey != "" {
			encrypted, err := AESEncrypt(body, encryptionKey)
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: server-side encryption failed for channel %s: %v\n", channel, err)
				http.Error(w, "encryption failed", http.StatusInternalServerError)
				return
			}
			payloadBytes = encrypted
		} else {
			payloadBytes = body
		}

		var headersBuilder strings.Builder
		payload := make(map[string]any)
		for k, v := range r.Header {
			fmt.Fprintf(&headersBuilder, " %s=%s", k, v[0])
			payload[strings.ToLower(k)] = v[0]
		}
		payload["timestamp"] = fmt.Sprintf("%d", now.UnixMilli())
		payload["bodyB"] = base64.StdEncoding.EncodeToString(payloadBytes)
		eventID := generateUUID()
		payload["event_id"] = eventID
		reencoded, err := json.Marshal(payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if chConfig.MessageTTLSeconds > 0 {
			broker.SetChannelTTL(channel, time.Duration(chConfig.MessageTTLSeconds)*time.Second)
		}

		publishEvent(broker, channel, reencoded)

		w.Header().Set(versionHeaderName, strings.TrimSpace(string(Version)))
		w.WriteHeader(http.StatusAccepted)
		resp := map[string]any{
			"status":   http.StatusAccepted,
			"channel":  channel,
			"message":  "ok",
			"version":  strings.TrimSpace(string(Version)),
			"event_id": eventID,
		}
		_ = json.NewEncoder(w).Encode(resp)
		fmt.Fprintf(os.Stdout, "%s Published %s%s on channel %s\n",
			now.Format(timeFormat),
			middleware.GetReqID(r.Context()),
			headersBuilder.String(),
			channel)
	}
}

func handleTestPayloadSend(broker *nats.Broker, rs *store.RaftStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channel := chi.URLParam(r, "channel")
		if channel == "" {
			http.Error(w, "Channel name missing in URL", http.StatusBadRequest)
			return
		}
		if len(channel) > maxChannelLength {
			http.Error(w, "Channel name exceeds maximum length", http.StatusBadRequest)
			return
		}

		now := time.Now().UTC()
		defer r.Body.Close()

		maxBodySize, _ := rs.ResolveChannelMaxBodySize(channel)
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

		if !json.Valid(body) {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		payload := make(map[string]any)
		payload["timestamp"] = fmt.Sprintf("%d", now.UnixMilli())
		payload["bodyB"] = base64.StdEncoding.EncodeToString(body)
		payload["content-type"] = contentType
		payload["x-test-payload"] = "true"

		reencoded, err := json.Marshal(payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		publishEvent(broker, channel, reencoded)

		w.WriteHeader(http.StatusAccepted)
		resp := map[string]any{
			"status":  http.StatusAccepted,
			"channel": channel,
			"message": "test payload sent",
		}
		_ = json.NewEncoder(w).Encode(resp)
		fmt.Fprintf(os.Stdout, "%s Test payload sent on channel %s\n",
			now.Format(timeFormat), channel)
	}
}

type ipRanges struct {
	networks []*net.IPNet
	ips      []net.IP
}

func parseIPRanges(ranges []string) (*ipRanges, error) {
	result := &ipRanges{}
	for _, r := range ranges {
		if strings.Contains(r, "/") {
			_, ipnet, err := net.ParseCIDR(r)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR range %q: %w", r, err)
			}
			result.networks = append(result.networks, ipnet)
		} else {
			ip := net.ParseIP(r)
			if ip == nil {
				return nil, fmt.Errorf("invalid IP address %q", r)
			}
			result.ips = append(result.ips, ip)
		}
	}
	return result, nil
}

func (r *ipRanges) contains(ip net.IP) bool {
	if slices.ContainsFunc(r.ips, func(allowedIP net.IP) bool {
		return ip.Equal(allowedIP)
	}) {
		return true
	}

	for _, network := range r.networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func getRealIP(r *http.Request, behindReverseProxy bool) (net.IP, error) {
	if behindReverseProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ips := strings.Split(xff, ",")
			clientIP := strings.TrimSpace(ips[0])
			ip := net.ParseIP(clientIP)
			if ip != nil {
				return ip, nil
			}
		}

		if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
			ip := net.ParseIP(strings.TrimSpace(xrip))
			if ip != nil {
				return ip, nil
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip := net.ParseIP(r.RemoteAddr)
		if ip != nil {
			return ip, nil
		}
		return nil, fmt.Errorf("invalid RemoteAddr %q: %w", r.RemoteAddr, err)
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP address %q", host)
	}
	return ip, nil
}

func ipRestrictMiddleware(rs *store.RaftStore) func(http.Handler) http.Handler {
	var (
		mu     sync.Mutex
		cache  = make(map[string]*ipRanges)
	)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}

			channel := chi.URLParam(r, "channel")
			allowedIPs, _ := rs.ResolveChannelAllowedIPs(channel)
			if len(allowedIPs) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			behindReverseProxy := rs.ResolveBehindReverseProxy()
			clientIP, err := getRealIP(r, behindReverseProxy)
			if err != nil {
				http.Error(w, "Failed to determine client IP", http.StatusBadRequest)
				return
			}

			mu.Lock()
			ranges, ok := cache[channel]
			mu.Unlock()
			if !ok {
				ranges, err = parseIPRanges(allowedIPs)
				if err != nil {
					http.Error(w, "Invalid IP configuration", http.StatusInternalServerError)
					return
				}
				mu.Lock()
				cache[channel] = ranges
				mu.Unlock()
			}

			if !ranges.contains(clientIP) {
				http.Error(w, fmt.Sprintf("IP address %s not allowed", clientIP), http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func retVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(versionHeaderName, strings.TrimSpace(string(Version)))
	resp := map[string]string{
		"version": strings.TrimSpace(string(Version)),
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		errorIt(w, nil, http.StatusInternalServerError, err)
	}
}

func handleEventsGet(broker *nats.Broker, protectedChannels *store.ProtectedChannels, rs *store.RaftStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channel := chi.URLParam(r, "channel")
		if channel == "" {
			http.Error(w, "Channel name missing in URL", http.StatusBadRequest)
			return
		}
		if len(channel) > maxChannelLength {
			http.Error(w, "Channel name exceeds maximum length", http.StatusBadRequest)
			return
		}

		corsOrigin := rs.ResolveCORSOrigin()

		var pubKey *[32]byte
		if protectedChannels.Has(channel) {
			pubKeyValue := r.URL.Query().Get("pubkey")
			if pubKeyValue == "" {
				rejectProtectedChannelRequest(w)
				return
			}

			var err error
			pubKey, err = ParsePublicKey(pubKeyValue)
			if err != nil || !protectedChannels.IsAllowed(channel, pubKey) {
				rejectProtectedChannelRequest(w)
				return
			}
		}
		if pubKey != nil {
			http.Error(w, "protected channels not supported with NATS broker", http.StatusNotImplemented)
			return
		}

		clientID := r.URL.Query().Get("client_id")

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		if corsOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", corsOrigin)
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, "data: %s\n\n", `{"message":"connected"}`)
		flusher.Flush()

		fmt.Fprintf(w, "data: %s\n\n", `{"message":"ready"}`)
		flusher.Flush()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		clientGone := r.Context().Done()

		var since time.Time
		if clientID != "" {
			cursor, _ := rs.GetClientCursor(channel, clientID)
			if cursor != nil && cursor.LastTimestampMs > 0 {
				since = time.UnixMilli(cursor.LastTimestampMs)
			}
		}
		var lastMsgTs int64
		historical, live := broker.Subscribe(channel, since, 100)
		defer broker.Unsubscribe(channel, live)

		for _, data := range historical {
			fmt.Fprintf(w, "data: %s\n\n", data)
			lastMsgTs = time.Now().UTC().UnixMilli()
			flusher.Flush()
		}

		sseLoop(w, flusher, live, clientGone, ticker, &lastMsgTs)

		if clientID != "" {
			lastTs := lastMsgTs
			if lastTs == 0 {
				lastTs = time.Now().UTC().UnixMilli()
			}
			if err := rs.SetClientCursor(&store.ClientCursor{
				Channel:         channel,
				ClientID:        clientID,
				LastTimestampMs: lastTs,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: failed to save client cursor for %s/%s: %v\n", channel, clientID, err)
			}
		}
	}
}

func sseLoop(w http.ResponseWriter, flusher http.Flusher, events <-chan []byte, clientGone <-chan struct{}, ticker *time.Ticker, lastMsgTs *int64) {
	for {
		select {
		case <-clientGone:
			return
		case data, ok := <-events:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			if lastMsgTs != nil {
				*lastMsgTs = time.Now().UTC().UnixMilli()
			}
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func handleEventReplay(broker *nats.Broker, rs *store.RaftStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channel := chi.URLParam(r, "channel")
		eventID := chi.URLParam(r, "eventId")
		if channel == "" || eventID == "" {
			http.Error(w, "channel and eventId required", http.StatusBadRequest)
			return
		}

		ch, err := rs.ResolveChannelConfig(channel)
		if err != nil {
			http.Error(w, "channel not found", http.StatusNotFound)
			return
		}
		if ch.EncryptionMode == "server_side" && ch.EncryptionKey != "" {
			http.Error(w, "cannot replay events for server-side encrypted channels via API", http.StatusBadRequest)
			return
		}

		payload := map[string]any{
			"timestamp": fmt.Sprintf("%d", time.Now().UTC().UnixMilli()),
			"event_id":  eventID,
			"bodyB":     base64.StdEncoding.EncodeToString([]byte(`{"replayed":true,"original_event_id":"`+eventID+`"}`)),
			"x-replay":  "true",
		}
		reencoded, _ := json.Marshal(payload)
		publishEvent(broker, channel, reencoded)

		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"status": "replayed", "event_id": eventID})
	}
}

func handleGenerateEncryptionKey(rs *store.RaftStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channel := chi.URLParam(r, "channel")
		ch, err := rs.GetChannel(channel)
		if err != nil {
			http.Error(w, "channel not found", http.StatusNotFound)
			return
		}

		var req struct {
			Mode string `json:"mode"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Mode == "" {
			req.Mode = "server_side"
		}

		switch req.Mode {
		case "server_side":
			key, err := GenerateAESKey()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			ch.EncryptionMode = "server_side"
			ch.EncryptionKey = key
			if err := rs.UpdateChannel(ch); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSONResponse(w, http.StatusOK, map[string]string{
				"encryption_key":  key,
				"encryption_mode": "server_side",
			})
		case "provider_side":
			pub, priv, err := GenerateKeyPair()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			ch.EncryptionMode = "provider_side"
			ch.EncryptionKey = base64.StdEncoding.EncodeToString(priv[:])
			pubKey := EncodePublicKey(pub)
			ch.EncryptionPubKeys = append(ch.EncryptionPubKeys, pubKey)
			if err := rs.UpdateChannel(ch); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSONResponse(w, http.StatusOK, map[string]any{
				"encryption_mode":       "provider_side",
				"encryption_public_key": pubKey,
			})
		default:
			http.Error(w, "unsupported encryption mode: "+req.Mode, http.StatusBadRequest)
		}
	}
}

func writeJSONResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func publishEvent(broker *nats.Broker, channel string, reencoded []byte) {
	if err := broker.Publish(channel, reencoded); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: nats publish error: %v\n", err)
	}
}

func serve(c *cli.Context) error {
	deprecatedEnvVars := map[string]string{
		"GOSMEE_WEBHOOK_SIGNATURE":       "--webhook-signature",
		"GOSMEE_ALLOWED_IPS":             "--allowed-ips",
		"GOSMEE_TRUST_PROXY":             "--trust-proxy (deprecated, use global config server.behind_reverse_proxy)",
		"GOSMEE_FOOTER":                  "--footer",
		"GOSMEE_ENCRYPTED_CHANNELS_FILE": "--encrypted-channels-file",
		"GOSMEE_CORS_ORIGIN":             "--cors-origin",
		"GOSMEE_MAX_BODY_SIZE":           "--max-body-size",
		"GOSMEE_AUTH_CONFIG_FILE":        "--auth-config-file",
		"GOSMEE_AUTH_SESSION_SECRET":     "--auth-session-secret",
	}
	for envVar, flag := range deprecatedEnvVars {
		if os.Getenv(envVar) != "" {
			fmt.Fprintf(os.Stderr, "FATAL: Environment variable %s is no longer supported (was %s flag).\n", envVar, flag)
			fmt.Fprintf(os.Stderr, "Configuration is now managed via Raft-stored config (bootstrap.yaml or Admin UI).\n")
			fmt.Fprintf(os.Stderr, "See README.md and SECURITY.md for migration instructions.\n")
			os.Exit(1)
		}
	}

	explicitPublicURL := c.String("public-url")

	rs, err := store.NewRaftStore(store.RaftConfig{
		Dir:           c.String("raft-dir"),
		NodeID:        c.String("raft-node-id"),
		BindAddr:      c.String("raft-bind-addr"),
		Peers:         c.StringSlice("raft-peers"),
		BootstrapPath: c.String("bootstrap-config-file"),
	})
	if err != nil {
		return fmt.Errorf("init raft store: %w", err)
	}

	protectedChannels := store.NewProtectedChannelsDynamic(rs)

	natsCfg := nats.Config{
		NodeID:      c.String("raft-node-id"),
		Port:        c.Int("nats-port"),
		ClusterPort: c.Int("nats-cluster-port"),
		Routes:      c.StringSlice("nats-routes"),
		BufferSize:  c.Int("nats-buffer-size"),
	}
	broker, natsErr := nats.New(natsCfg)
	if natsErr != nil {
		return fmt.Errorf("init nats broker: %w", natsErr)
	}
	defer broker.Shutdown()

	channels, _ := rs.ListChannels()
	for _, ch := range channels {
		resolved, _ := rs.ResolveChannelConfig(ch.ID)
		if resolved.MessageTTLSeconds > 0 {
			broker.SetChannelTTL(ch.ID, time.Duration(resolved.MessageTTLSeconds)*time.Second)
		}
	}

	if c.Bool("dev-admin") {
		if err := initDevAdmin(rs, c.String("dev-admin-password"), c.String("raft-dir")); err != nil {
			return fmt.Errorf("dev admin: %w", err)
		}
	}

	autoCert := c.Bool("auto-cert")
	certFile := c.String("tls-cert")
	certKey := c.String("tls-key")
	sslEnabled := certFile != "" && certKey != ""
	portAddr := fmt.Sprintf("%s:%d", c.String("address"), c.Int("port"))
	publicURL := effectivePublicURL(explicitPublicURL, portAddr, sslEnabled)

	mainRouter := chi.NewRouter()
	restrictedRouter := chi.NewRouter()

	mainRouter.Use(middleware.RequestID)
	mainRouter.Use(middleware.Logger)
	mainRouter.Use(middleware.Recoverer)

	restrictedRouter.Use(middleware.RequestID)
	restrictedRouter.Use(middleware.Logger)
	restrictedRouter.Use(middleware.Recoverer)

	restrictedRouter.Use(ipRestrictMiddleware(rs))

	// Always set up session secret if not already configured
	secret := rs.SessionSecret()
	if secret == "" {
		if rs.IsLeader() {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				return fmt.Errorf("generate session secret: %w", err)
			}
			secret = hex.EncodeToString(b)
			if err := rs.SetSessionSecret(secret); err != nil {
				return fmt.Errorf("persist generated session secret: %w", err)
			}
			fmt.Fprintf(os.Stderr, "WARNING: Generated random session secret and stored in Raft\n")
		} else {
			// Check if there are any users — if so, session secret is required
			users, _ := rs.ListUsers()
			providers, _ := rs.OIDCProviders()
			if len(users) > 0 || len(providers) > 0 {
				return fmt.Errorf("no session secret configured and node is not the leader: set session_secret via bootstrap.yaml or on the leader node")
			}
		}
	}
	if secret != "" {
		sessionSecret = deriveSessionSecret(secret)
	}

	// Unprotected routes
	mainRouter.Get("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(faviconSVG)
	})
	mainRouter.Get("/version", retVersion)
	mainRouter.Get("/health", retVersion)
	mainRouter.Get("/livez", retVersion)

	mainRouter.Get(eventsPath, handleEventsGet(broker, protectedChannels, rs))

	// OIDC routes (registered dynamically from Raft config)
	providers, _ := rs.OIDCProviders()
	for _, provider := range providers {
		oidcHandler, err := NewOIDCHandler(provider, sessionSecret, publicURL)
		if err != nil {
			return fmt.Errorf("init OIDC handler for %s: %w", provider.ID, err)
		}
		mainRouter.Get("/auth/oidc/"+provider.ID+"/login", oidcHandler.LoginHandler())
		mainRouter.Get("/auth/oidc/"+provider.ID+"/callback", oidcHandler.CallbackHandler())
	}

	// SPA handler — all unmatched GET routes serve the SPA
	mainRouter.NotFound(spaHandler().ServeHTTP)

	// POST routes on restricted router
	restrictedRouter.Post(channelPath, handleWebhookPost(broker, rs))

	// Public auth API routes — mounted before main /api to avoid middleware intercept
	publicApiRouter := chi.NewRouter()
	publicApiRouter.Get("/methods", apiAuthMethodsHandler(rs))
	publicApiRouter.Post("/login", apiLoginHandler(rs))
	publicApiRouter.Post("/logout", apiLogoutHandler())
	mainRouter.Mount("/api/auth", publicApiRouter)

	// API routes — dynamic auth handles setup mode and authentication
	apiRouter := chi.NewRouter()
	apiRouter.Use(RequireAuthDynamic(rs))
	notifier := &brokerTTLNotifier{broker: broker, rs: rs}
	store.RegisterAPIHandlers(apiRouter, rs, notifier)
	apiRouter.Post("/send/{channel:"+channelIDPattern+"}", handleTestPayloadSend(broker, rs))
	apiRouter.Post("/channels/{channel:"+channelIDPattern+"}/events/{eventId}/replay", handleEventReplay(broker, rs))
	apiRouter.Post("/channels/{channel:"+channelIDPattern+"}/generate-encryption-key", handleGenerateEncryptionKey(rs))
	mainRouter.Mount("/api", apiRouter)

	finalRouter := chi.NewRouter()

	finalRouter.Mount("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !strings.HasPrefix(r.URL.Path, "/api/") {
			restrictedRouter.ServeHTTP(w, r)
		} else {
			mainRouter.ServeHTTP(w, r)
		}
	}))

	fmt.Fprintf(os.Stdout, "Serving for webhooks on %s\n", publicURL)

	if sslEnabled {
		return http.ListenAndServeTLS(portAddr, certFile, certKey, finalRouter)
	} else if autoCert {
		return http.Serve(autocert.NewListener(publicURL), finalRouter)
	}
	return http.ListenAndServe(portAddr, finalRouter)
}

type brokerTTLNotifier struct {
	broker *nats.Broker
	rs     *store.RaftStore
}

func (n *brokerTTLNotifier) OnChannelChanged(channelID string, ttlSeconds int) {
	if ttlSeconds > 0 {
		n.broker.SetChannelTTL(channelID, time.Duration(ttlSeconds)*time.Second)
	}
}

func initDevAdmin(rs *store.RaftStore, password, raftDir string) error {
	if !rs.IsSetupMode() {
		return nil
	}
	if password == "" {
		password = generateRandomHex(16)
	}
	if err := rs.CreateDevAdmin(password); err != nil {
		return fmt.Errorf("create dev admin: %w", err)
	}
	passwordFile := raftDir + "/admin-password.txt"
	if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0600); err != nil {
		return fmt.Errorf("write password file: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Dev admin account created. Username: admin. Password saved to %s\n", passwordFile)
	return nil
}