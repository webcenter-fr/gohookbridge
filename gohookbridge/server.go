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
	"github.com/r3labs/sse/v2"
	"github.com/urfave/cli/v2"
	"golang.org/x/crypto/acme/autocert"
)

const (
	timeFormat        = "2006-01-02T15.04.01.000"
	contentType       = "application/json"
	versionHeaderName = "X-Gosmee-Version"
	minChannelLength  = 12
	maxChannelLength  = 64
	channelIDPattern  = "[a-zA-Z0-9_-]{12,64}"
	channelPath       = "/{channel:" + channelIDPattern + "}"
	eventsPath        = "/events/{channel:" + channelIDPattern + "}"
	replayPath        = "/replay/{channel:" + channelIDPattern + "}"
)

var (
	defaultServerPort    = 3333
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

func validateWebhookSignature(secrets []string, payload []byte, r *http.Request) bool {
	if len(secrets) == 0 {
		return true
	}

	if gitlabToken := r.Header.Get("X-Gitlab-Token"); gitlabToken != "" {
		for _, secret := range secrets {
			if subtle.ConstantTimeCompare([]byte(gitlabToken), []byte(secret)) == 1 {
				return true
			}
		}
		return false
	}

	if githubSignature := r.Header.Get("X-Hub-Signature-256"); githubSignature != "" {
		fmt.Fprintf(os.Stdout, "Received request %s %s\n", r.Method, r.URL.Path)
		for _, secret := range secrets {
			if validateGitHubWebhookSignature(secret, payload, githubSignature) {
				return true
			}
		}
		return false
	}

	if bitbucketSignature := r.Header.Get("X-Hub-Signature"); bitbucketSignature != "" {
		for _, secret := range secrets {
			if validateBitbucketHMAC(secret, payload, bitbucketSignature) {
				return true
			}
		}
		return false
	}

	if giteaSignature := r.Header.Get("X-Gitea-Signature"); giteaSignature != "" {
		for _, secret := range secrets {
			if validateGiteaSignature(secret, payload, giteaSignature) {
				return true
			}
		}
		return false
	}

	return false
}

func handleWebhookPost(events *sse.Server, eventBroker *EventBroker, broker *nats.Broker, rs *store.RaftStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		if !strings.Contains(r.Header.Get("Content-Type"), contentType) {
			http.Error(w, "content-type must be application/json", http.StatusBadRequest)
			return
		}
		channel := chi.URLParam(r, "channel")
		defer r.Body.Close()

		maxBodySize, _ := rs.ResolveProjectMaxBodySize(channel)
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

		webhookSecrets, _ := rs.ResolveProjectSignatures(channel)
		if len(webhookSecrets) > 0 {
			if !validateWebhookSignature(webhookSecrets, body, r) {
				http.Error(w, "invalid signature", http.StatusUnauthorized)
				return
			}
		}

		var d any
		if err := json.Unmarshal(body, &d); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var headersBuilder strings.Builder
		payload := make(map[string]any)
		for k, v := range r.Header {
			fmt.Fprintf(&headersBuilder, " %s=%s", k, v[0])
			payload[strings.ToLower(k)] = v[0]
		}
		payload["timestamp"] = fmt.Sprintf("%d", now.UnixMilli())
		payload["bodyB"] = base64.StdEncoding.EncodeToString(body)
		reencoded, err := json.Marshal(payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if broker != nil {
			if err := broker.Publish(channel, reencoded); err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: nats publish error: %v\n", err)
			}
		} else {
			events.CreateStream(channel)
			events.Publish(channel, &sse.Event{Data: reencoded})

			eventBroker.Publish(channel, reencoded)
		}

		w.Header().Set(versionHeaderName, strings.TrimSpace(string(Version)))

		w.WriteHeader(http.StatusAccepted)
		resp := map[string]any{
			"status":  http.StatusAccepted,
			"channel": channel,
			"message": "ok",
			"version": strings.TrimSpace(string(Version)),
		}
		_ = json.NewEncoder(w).Encode(resp)
		fmt.Fprintf(os.Stdout, "%s Published %s%s on channel %s\n",
			now.Format(timeFormat),
			middleware.GetReqID(r.Context()),
			headersBuilder.String(),
			channel)
	}
}

func handleReplayPost(events *sse.Server, eventBroker *EventBroker, broker *nats.Broker, rs *store.RaftStore) http.HandlerFunc {
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

		authorizationHeader := r.Header.Get("Authorization")
		token := ""
		if strings.HasPrefix(authorizationHeader, "Bearer ") {
			token = strings.TrimPrefix(authorizationHeader, "Bearer ")
		}
		if !rs.ValidateReplayToken(channel, token) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		now := time.Now().UTC()
		maxBodySize, _ := rs.ResolveProjectMaxBodySize(channel)
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

		payload := make(map[string]any)
		for k, v := range r.Header {
			if strings.EqualFold(k, "Authorization") {
				continue
			}
			payload[strings.ToLower(k)] = v[0]
		}
		payload["timestamp"] = fmt.Sprintf("%d", now.UnixMilli())
		payload["bodyB"] = base64.StdEncoding.EncodeToString(body)
		payload["content-type"] = contentType

		reencoded, err := json.Marshal(payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if broker != nil {
			if err := broker.Publish(channel, reencoded); err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: nats publish error: %v\n", err)
			}
		} else {
			events.CreateStream(channel)
			events.Publish(channel, &sse.Event{Data: reencoded})
			eventBroker.Publish(channel, reencoded)
		}

		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("replayed"))
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

func getRealIP(r *http.Request, trustProxy bool) (net.IP, error) {
	if trustProxy {
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
			allowedIPs, _ := rs.ResolveProjectAllowedIPs(channel)
			if len(allowedIPs) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			trustProxy := rs.ResolveTrustProxy()
			clientIP, err := getRealIP(r, trustProxy)
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

func handleEventsGet(eventBroker *EventBroker, broker *nats.Broker, protectedChannels *store.ProtectedChannels, rs *store.RaftStore) http.HandlerFunc {
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
		if broker != nil && pubKey != nil {
			http.Error(w, "protected channels not supported with NATS broker", http.StatusNotImplemented)
			return
		}

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

		if broker != nil {
			historical, live := broker.Subscribe(channel, 100)
			defer broker.Unsubscribe(channel, live)

			for _, data := range historical {
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}

			sseLoop(w, flusher, live, clientGone, ticker)
		} else {
			subscriber := eventBroker.Subscribe(channel, pubKey)
			defer eventBroker.Unsubscribe(channel, subscriber)

			sseLoop(w, flusher, subscriber.Events, clientGone, ticker)
		}
	}
}

func sseLoop(w http.ResponseWriter, flusher http.Flusher, events <-chan []byte, clientGone <-chan struct{}, ticker *time.Ticker) {
	for {
		select {
		case <-clientGone:
			return
		case data, ok := <-events:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func serve(c *cli.Context) error {
	deprecatedEnvVars := map[string]string{
		"GOSMEE_WEBHOOK_SIGNATURE":       "--webhook-signature",
		"GOSMEE_REPLAY_TOKEN":            "--replay-token",
		"GOSMEE_ALLOWED_IPS":             "--allowed-ips",
		"GOSMEE_TRUST_PROXY":             "--trust-proxy",
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

	events := sse.New()
	events.AutoReplay = false
	events.AutoStream = true
	eventBroker := NewEventBroker()

	natsCfg := nats.Config{
		NodeID:      c.String("raft-node-id"),
		Port:        c.Int("nats-port"),
		ClusterPort: c.Int("nats-cluster-port"),
		Routes:      c.StringSlice("nats-routes"),
		BufferTTL:   c.Duration("nats-buffer-ttl"),
		BufferSize:  c.Int("nats-buffer-size"),
	}
	broker, natsErr := nats.New(natsCfg)
	if natsErr != nil {
		return fmt.Errorf("init nats broker: %w", natsErr)
	}
	if broker != nil {
		defer broker.Shutdown()
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

	mainRouter.Get(eventsPath, handleEventsGet(eventBroker, broker, protectedChannels, rs))

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

	// Public API auth endpoints (no auth middleware)
	mainRouter.Get("/api/auth/methods", apiAuthMethodsHandler(rs))
	mainRouter.Post("/api/auth/login", apiLoginHandler(rs))
	mainRouter.Post("/api/auth/logout", apiLogoutHandler())

	// SPA handler — all unmatched GET routes serve the SPA
	mainRouter.NotFound(spaHandler().ServeHTTP)

	// POST routes on restricted router
	restrictedRouter.Post(channelPath, handleWebhookPost(events, eventBroker, broker, rs))
	restrictedRouter.Post(replayPath, handleReplayPost(events, eventBroker, broker, rs))

	// API routes — dynamic auth handles setup mode and authentication
	apiRouter := chi.NewRouter()
	apiRouter.Use(RequireAuthDynamic(rs))
	store.RegisterAPIHandlers(apiRouter, rs)
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