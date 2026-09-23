// Package server is the composition root: it wires the repository, service,
// handler, and NATS packages together. It is the only package allowed to
// import all layers.
package server

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/urfave/cli/v2"
	"github.com/webcenter-fr/gohookbridge/internal/app"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/handler"
	"github.com/webcenter-fr/gohookbridge/internal/repository"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"github.com/webcenter-fr/gohookbridge/internal/web"
	"github.com/webcenter-fr/gohookbridge/pkg/nats"
	"golang.org/x/crypto/acme/autocert"
)

//go:embed templates/favicon.svg
var faviconSVG []byte

// Server is the assembled application: it holds every wired component so Run
// can serve HTTP.
type Server struct {
	repo          *repository.RaftStore
	svc           *service.Service
	broker        *nats.Broker
	rateLimiter   *service.RateLimiter
	banTracker    *service.BanTracker
	sessionSecret [32]byte
	httpServer    *http.Server
	certFile      string
	certKey       string
	autoCert      bool
	publicURL     string
	cancelCtx     context.CancelFunc
}

// NewServer performs the full dependency-injection wiring formerly done by
// serve(): it checks for deprecated env vars, starts Raft, applies the
// bootstrap config, starts NATS, resolves the session secret, and assembles
// the HTTP router. Call Run to serve.
func NewServer(c *cli.Context) (*Server, error) {
	//nolint:gosec
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

	ctx := context.Background()
	explicitPublicURL := c.String("public-url")

	hostname, _ := os.Hostname()
	discovery := repository.RaftDiscoveryConfig{
		NodeID:          c.String("raft-node-id"),
		AdvertiseAddr:   c.String("raft-advertise-addr"),
		BindAddr:        c.String("raft-bind-addr"),
		Peers:           toRaftPeers(c.StringSlice("raft-peers")),
		Replicas:        c.Int("raft-replicas"),
		StatefulSetName: c.String("raft-statefulset-name"),
		HeadlessService: c.String("raft-headless-service"),
		Namespace:       effectiveNamespace(c),
		ClusterDomain:   c.String("raft-cluster-domain"),
	}
	resolver := repository.NewPeerResolver(&discovery)
	advertise, err := repository.DeriveAdvertiseAddr(&discovery, hostname)
	if err != nil {
		return nil, fmt.Errorf("derive raft advertise addr: %w", err)
	}
	raftTLS, err := buildRaftTLSConfig(c, discovery, hostname)
	if err != nil {
		return nil, fmt.Errorf("build raft TLS config: %w", err)
	}

	leaderWaitTimeout := c.Duration("raft-leader-wait-timeout")

	// Automatic single-node collapse: when the operator scales the StatefulSet
	// down to one replica, Raft cannot commit the membership change that
	// removes the lost voters (no quorum). The surviving bootstrap node forces
	// the configuration instead, which is only authorized by an explicit
	// spec.replicas == 1 on the StatefulSet. Multi-voter deployments are
	// unaffected and still require --raft-recovery-mode for manual quorum loss.
	singleNodeRecovery := false
	var replicaReader *statefulSetReplicaReader
	if discovery.StatefulSetName != "" && discovery.Namespace != "" && c.Int("raft-replicas") > 1 {
		replicaReader = newStatefulSetReplicaReader(discovery.Namespace, discovery.StatefulSetName)
		if replicas, ok := replicaReader.replicas(ctx); ok && replicas == 1 {
			singleNodeRecovery = true
			log.Printf("WARNING: StatefulSet %s/%s has 1 replica; enabling single-node Raft recovery", discovery.Namespace, discovery.StatefulSetName)
		}
	}

	// Generation fencing: a recovery bumps the cluster generation shared
	// through the CA Secret (bootstrap node only). A node that starts with an
	// older generation still holds a pre-recovery configuration and could form
	// a separate quorum with other stale nodes; it clears its Raft state and
	// rejoins the bootstrap node instead.
	generationStore := newRaftGenerationStore(effectiveNamespace(c), c.String("raft-tls-ca-secret"))
	bootstrapNode := strings.HasSuffix(hostname, "-0") || c.Int("raft-replicas") <= 1
	var clusterGeneration uint64
	if generationStore != nil {
		clusterGeneration, _ = generationStore.read(ctx)
	}
	localGeneration, _ := readLocalGeneration(c.String("raft-dir"))
	if generationStore != nil && bootstrapNode && (singleNodeRecovery || c.Bool("raft-recovery-mode")) {
		if next, genErr := generationStore.bump(ctx); genErr != nil {
			log.Printf("WARNING: raft generation bump failed (%v); stale nodes may not rejoin cleanly", genErr)
		} else {
			clusterGeneration = next
			localGeneration = next
		}
	}
	staleGeneration := !bootstrapNode && clusterGeneration > localGeneration
	if staleGeneration {
		log.Printf("WARNING: raft generation %d supersedes local %d; clearing stale Raft state to rejoin", clusterGeneration, localGeneration)
	}

	rs, err := repository.NewRaftStore(repository.RaftConfig{
		Dir:                   c.String("raft-dir"),
		NodeID:                c.String("raft-node-id"),
		BindAddr:              c.String("raft-bind-addr"),
		AdvertiseAddr:         advertise,
		Peers:                 c.StringSlice("raft-peers"),
		BootstrapPath:         c.String("bootstrap-config-file"),
		Replicas:              c.Int("raft-replicas"),
		StatefulSetName:       c.String("raft-statefulset-name"),
		HeadlessService:       c.String("raft-headless-service"),
		Namespace:             effectiveNamespace(c),
		ClusterDomain:         c.String("raft-cluster-domain"),
		RecoveryMode:          c.Bool("raft-recovery-mode") || staleGeneration,
		SingleNodeRecovery:    singleNodeRecovery,
		NoSnapshotRestore:     c.Bool("raft-no-snapshot-restore"),
		PerformanceMultiplier: c.Float64("raft-performance-multiplier"),
		ApplyTimeout:          10 * time.Second,
		Resolver:              resolver,
		TLS:                   raftTLS,
	})
	if err != nil {
		return nil, fmt.Errorf("init raft store: %w", err)
	}
	startupCtx, cancelStartup := context.WithCancel(context.Background())
	if generationStore != nil {
		if err := writeLocalGeneration(c.String("raft-dir"), clusterGeneration); err != nil {
			log.Printf("WARNING: persist raft generation: %v", err)
		}
	}

	// Wait for ANY leader (followers must not CrashLoop waiting for self
	// leadership), then start the leader-only membership join loop.
	leaderCtx, cancelLeader := context.WithTimeout(startupCtx, leaderWaitTimeout)
	defer cancelLeader()
	if err := rs.WaitForLeader(leaderCtx); err != nil {
		cancelStartup()
		_ = rs.Shutdown()
		return nil, fmt.Errorf("wait for raft leader: %w", err)
	}
	go rs.StartJoinLoop(startupCtx)
	if replicaReader != nil && strings.HasSuffix(hostname, "-0") {
		go watchSingleNodeRecovery(startupCtx, replicaReader, rs, leaderWaitTimeout)
	}
	if err := rs.WaitForCleanState(leaderCtx); err != nil {
		cancelStartup()
		_ = rs.Shutdown()
		return nil, fmt.Errorf("wait for raft clean state: %w", err)
	}

	// Apply bootstrap.yaml exactly once, on the leader, when the FSM is empty.
	if err := applyBootstrapOnce(ctx, rs, c.String("bootstrap-config-file")); err != nil {
		cancelStartup()
		_ = rs.Shutdown()
		return nil, err
	}

	if rs.IsLeader() {
		if err := rs.MigrateRBAC(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: rbac migration error (server will continue): %v\n", err)
		}
	}

	natsCfg := nats.Config{
		NodeID:      c.String("raft-node-id"),
		Port:        c.Int("nats-port"),
		ClusterPort: c.Int("nats-cluster-port"),
		Routes:      c.StringSlice("nats-routes"),
		BufferSize:  c.Int("nats-buffer-size"),
		ClusterName: c.String("nats-cluster-name"),
	}
	broker, natsErr := nats.New(natsCfg)
	if natsErr != nil {
		cancelStartup()
		_ = rs.Shutdown()
		return nil, fmt.Errorf("init nats broker: %w", natsErr)
	}

	svc := service.NewService(rs, &brokerTTLNotifier{broker: broker})

	channels, _ := svc.ListChannels(ctx)
	for _, ch := range channels {
		resolved, _ := svc.ResolveChannelConfig(ctx, ch.ID)
		if resolved.MessageTTLSeconds > 0 {
			broker.SetChannelTTL(ch.ID, time.Duration(resolved.MessageTTLSeconds)*time.Second)
		}
	}

	if c.Bool("dev-admin") {
		if err := initDevAdmin(ctx, svc, c.String("dev-admin-password"), c.String("raft-dir")); err != nil {
			cancelStartup()
			broker.Shutdown()
			_ = rs.Shutdown()
			return nil, fmt.Errorf("dev admin: %w", err)
		}
	}

	rateLimiterInst := service.NewRateLimiter()
	banTrackerInst := service.NewBanTracker()

	autoCert := c.Bool("auto-cert")
	certFile := c.String("tls-cert")
	certKey := c.String("tls-key")
	sslEnabled := certFile != "" && certKey != ""
	portAddr := fmt.Sprintf("%s:%d", c.String("address"), c.Int("port"))
	publicURL := handler.EffectivePublicURL(explicitPublicURL, portAddr, sslEnabled)

	// Session cookies carry the Secure flag only when the effective
	// deployment is TLS (manual certs, auto-cert, or an https public URL);
	// plain-HTTP dev deployments keep working.
	cookieSecure := sslEnabled || autoCert || strings.HasPrefix(publicURL, "https://")

	// Session secret: the leader generates and replicates it; followers poll
	// for the replicated value before requiring it.
	secret := svc.SessionSecret(ctx)
	if secret == "" {
		if rs.IsLeader() {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				cancelStartup()
				broker.Shutdown()
				_ = rs.Shutdown()
				return nil, fmt.Errorf("generate session secret: %w", err)
			}
			secret = hex.EncodeToString(b)
			if err := svc.SetSessionSecret(ctx, secret); err != nil {
				cancelStartup()
				broker.Shutdown()
				_ = rs.Shutdown()
				return nil, fmt.Errorf("persist generated session secret: %w", err)
			}
			fmt.Fprintf(os.Stderr, "WARNING: Generated random session secret and stored in Raft\n")
		} else {
			deadline := time.Now().Add(leaderWaitTimeout)
			for secret == "" && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
				secret = svc.SessionSecret(ctx)
			}
			if secret == "" {
				// Check if there are any users — if so, session secret is required
				users, _ := svc.ListUsers(ctx)
				providers, _ := svc.OIDCProviders(ctx)
				if len(users) > 0 || len(providers) > 0 {
					cancelStartup()
					broker.Shutdown()
					_ = rs.Shutdown()
					return nil, fmt.Errorf("no session secret configured and node is not the leader: set session_secret via bootstrap.yaml or on the leader node")
				}
			}
		}
	}
	var sessionSecret [32]byte
	if secret != "" {
		if err := service.ValidateSessionSecret(secret); err != nil {
			// Legacy stored secrets that are weaker than the current minimum
			// must not crash-loop existing deployments: warn and keep serving.
			fmt.Fprintf(os.Stderr, "WARNING: stored session_secret is weak: %v (rotate before relying on it)\n", err)
		}
		sessionSecret = service.DeriveSessionSecret(secret)
	}

	handler.Version = app.Version

	mainRouter := chi.NewRouter()
	restrictedRouter := chi.NewRouter()

	mainRouter.Use(middleware.RequestID)
	mainRouter.Use(handler.SafeLogger)
	mainRouter.Use(middleware.Recoverer)
	mainRouter.Use(handler.BanMiddleware(banTrackerInst, svc))
	mainRouter.Use(handler.RateLimitMiddleware(rateLimiterInst, svc))

	restrictedRouter.Use(middleware.RequestID)
	restrictedRouter.Use(handler.SafeLogger)
	restrictedRouter.Use(middleware.Recoverer)
	restrictedRouter.Use(handler.BanMiddleware(banTrackerInst, svc))
	restrictedRouter.Use(handler.RateLimitMiddleware(rateLimiterInst, svc))
	restrictedRouter.Use(handler.IPRestrictMiddleware(svc))

	// Unprotected routes
	mainRouter.Get("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(faviconSVG)
	})
	mainRouter.Get("/version", handler.RetVersion)
	mainRouter.Get("/health", handler.RetVersion)
	mainRouter.Get("/livez", handler.RetVersion)
	mainRouter.Get("/readyz", handler.RetReadyz(rs))
	mainRouter.Get("/startup", handler.RetStartup(rs))

	mainRouter.Get(handler.EventsPath, handler.ChannelAccessMiddleware(svc, "consume", banTrackerInst)(handler.HandleEventsGet(broker, svc)).ServeHTTP)

	// OIDC routes (registered dynamically from Raft config)
	providers, _ := svc.OIDCProviders(ctx)
	for _, provider := range providers {
		oidcHandler, err := handler.NewOIDCHandler(provider, sessionSecret, publicURL, cookieSecure)
		if err != nil {
			cancelStartup()
			broker.Shutdown()
			_ = rs.Shutdown()
			return nil, fmt.Errorf("init OIDC handler for %s: %w", provider.ID, err)
		}
		mainRouter.Get("/auth/oidc/"+provider.ID+"/login", oidcHandler.LoginHandler())
		mainRouter.Get("/auth/oidc/"+provider.ID+"/callback", oidcHandler.CallbackHandler())
	}

	// SPA handler — all unmatched GET routes serve the SPA
	mainRouter.NotFound(web.SPAHandler().ServeHTTP)

	// POST routes on restricted router
	restrictedRouter.Use(handler.ChannelAccessMiddleware(svc, "produce", banTrackerInst))
	restrictedRouter.Post(handler.ChannelPath, handler.HandleWebhookPost(broker, svc, banTrackerInst))

	// Public auth API routes — mounted before main /api to avoid middleware intercept
	publicAPIRouter := chi.NewRouter()
	publicAPIRouter.Get("/methods", handler.APIAuthMethodsHandler(svc))
	publicAPIRouter.Post("/login", handler.APILoginHandler(svc, sessionSecret, banTrackerInst, cookieSecure))
	publicAPIRouter.Post("/logout", handler.APILogoutHandler(cookieSecure))
	mainRouter.Mount("/api/auth", publicAPIRouter)

	// API routes — dynamic auth handles setup mode and authentication
	apiRouter := chi.NewRouter()
	// Mutating requests must reach the Raft leader; in an HA deployment the
	// Service load-balances over all replicas, so followers forward writes.
	apiRouter.Use(handler.LeaderForwardMiddleware(rs, c.Int("port")))
	apiRouter.Use(handler.RequireAuthDynamic(svc, sessionSecret, cookieSecure))
	handler.RegisterAPIHandlers(apiRouter, svc)
	// Channel-write-gated operational endpoints (no setup-mode bypass: in setup
	// mode there is no session, so RequirePermission returns 401).
	apiRouter.Group(func(r chi.Router) {
		r.Use(handler.ChannelContext) // first Use = outermost
		r.Use(handler.RequirePermission(svc, domain.PermChannelWrite))
		r.Post("/send/{channel:"+handler.ChannelIDPattern+"}", handler.HandleTestPayloadSend(broker, svc))
		r.Post("/channels/{channel:"+handler.ChannelIDPattern+"}/events/{eventId:"+handler.EventIDPattern+"}/replay", handler.HandleEventReplay(broker, svc))
	})
	// Admin-only endpoint.
	apiRouter.Group(func(r chi.Router) {
		r.Use(handler.RequirePermission(svc, domain.PermAll))
		r.Post("/channels/{channel:"+handler.ChannelIDPattern+"}/generate-encryption-key", handler.HandleGenerateEncryptionKey(svc))
	})
	apiRouter.Group(func(r chi.Router) {
		r.Use(handler.RequirePermission(svc, domain.PermGlobalRead))
		r.Get("/bans", handler.APIBansHandler(banTrackerInst))
		r.With(handler.RequirePermission(svc, domain.PermGlobalWrite)).Delete("/bans/{ip}", handler.APIUnbanHandler(banTrackerInst))
	})
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

	return &Server{
		repo:          rs,
		svc:           svc,
		broker:        broker,
		rateLimiter:   rateLimiterInst,
		banTracker:    banTrackerInst,
		sessionSecret: sessionSecret,
		publicURL:     publicURL,
		certFile:      certFile,
		certKey:       certKey,
		autoCert:      autoCert,
		cancelCtx:     cancelStartup,
		httpServer: &http.Server{
			Addr:              portAddr,
			Handler:           finalRouter,
			ReadHeaderTimeout: 10 * time.Second,
		},
	}, nil
}

// Run serves HTTP until ctx is canceled or a signal arrives, then shuts down
// gracefully (transferring Raft leadership).
func (s *Server) Run(ctx context.Context) error {
	defer func() {
		s.cancelCtx()
		_ = s.repo.Shutdown()
		s.broker.Shutdown()
	}()

	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Graceful leave: on SIGTERM transfer Raft leadership before shutting the
	// HTTP server down, so a rolling restart re-elects a leader promptly.
	go func() {
		<-runCtx.Done()
		stepDownCtx, cancel := context.WithTimeout(context.WithoutCancel(runCtx), 10*time.Second)
		defer cancel()
		if err := s.repo.StepDown(stepDownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: raft step down: %v\n", err)
		}
		shutdownCtx, cancelShutdown := context.WithTimeout(context.WithoutCancel(runCtx), 10*time.Second)
		defer cancelShutdown()
		_ = s.httpServer.Shutdown(shutdownCtx)
	}()

	var serveErr error
	switch {
	case s.certFile != "" && s.certKey != "":
		serveErr = s.httpServer.ListenAndServeTLS(s.certFile, s.certKey)
	case s.autoCert:
		serveErr = s.httpServer.Serve(autocert.NewListener(s.publicURL))
	default:
		serveErr = s.httpServer.ListenAndServe()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}

// applyBootstrapOnce applies bootstrap.yaml exactly once: only on the leader,
// and only while the FSM is still empty. ApplyBootstrap also guards HasData
// internally; the leader-only + once semantics guarantee a single application.
func applyBootstrapOnce(ctx context.Context, rs *repository.RaftStore, path string) error {
	if path == "" || !rs.IsLeader() {
		return nil
	}
	hasData, err := rs.HasData()
	if err != nil {
		return err
	}
	if hasData {
		return nil
	}
	cfg, err := repository.LoadBootstrap(path)
	if err != nil {
		return fmt.Errorf("load bootstrap: %w", err)
	}
	if cfg.Global != nil && cfg.Global.Server.SessionSecret != "" {
		if err := service.ValidateSessionSecret(cfg.Global.Server.SessionSecret); err != nil {
			return fmt.Errorf("bootstrap session_secret: %w", err)
		}
	}
	if err := rs.ApplyBootstrap(ctx, cfg); err != nil {
		return fmt.Errorf("apply bootstrap: %w", err)
	}
	return nil
}

// toRaftPeers parses legacy "id=addr" peer entries into discovery peers.
func toRaftPeers(entries []string) []repository.RaftPeer {
	peers := make([]repository.RaftPeer, 0, len(entries))
	for _, e := range entries {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		peers = append(peers, repository.RaftPeer{ID: parts[0], Address: parts[1]})
	}
	return peers
}

// watchSingleNodeRecovery restarts the bootstrap node when the Raft cluster
// has been leaderless for leaderWaitTimeout while the StatefulSet is scaled to
// a single replica. A 3-voter cluster whose other two pods were removed cannot
// elect a leader or commit a membership change; the restart re-enters serve(),
// which sees spec.replicas == 1 and collapses the configuration to this node
// via RecoverCluster. The Kubernetes replica count is the authorization: a
// multi-replica deployment (e.g. two pods temporarily down) never triggers
// this path.
func watchSingleNodeRecovery(ctx context.Context, reader *statefulSetReplicaReader, rs *repository.RaftStore, leaderWaitTimeout time.Duration) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var leaderlessSince time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if rs.IsLeader() || rs.LeaderAddress() != "" {
				leaderlessSince = time.Time{}
				continue
			}
			if leaderlessSince.IsZero() {
				leaderlessSince = time.Now()
				continue
			}
			if time.Since(leaderlessSince) < leaderWaitTimeout {
				continue
			}
			replicas, ok := reader.replicas(ctx)
			if !ok || replicas != 1 {
				continue
			}
			log.Printf("WARNING: no Raft leader for %s while the StatefulSet has 1 replica; restarting to force single-node recovery", leaderWaitTimeout)
			_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
			return
		}
	}
}

type brokerTTLNotifier struct {
	broker *nats.Broker
}

func (n *brokerTTLNotifier) OnChannelChanged(channelID string, ttlSeconds int) {
	if ttlSeconds > 0 {
		n.broker.SetChannelTTL(channelID, time.Duration(ttlSeconds)*time.Second)
	}
}

func initDevAdmin(ctx context.Context, svc *service.Service, password, raftDir string) error {
	if !svc.IsSetupMode(ctx) {
		return nil
	}
	if password == "" {
		var err error
		password, err = service.GenerateRandomHex()
		if err != nil {
			return fmt.Errorf("generate dev admin password: %w", err)
		}
	}
	if err := svc.CreateDevAdmin(ctx, password); err != nil {
		return fmt.Errorf("create dev admin: %w", err)
	}
	passwordFile := raftDir + "/admin-password.txt"
	if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0600); err != nil {
		return fmt.Errorf("write password file: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Dev admin account created. Username: admin. Password saved to %s\n", passwordFile)
	return nil
}
