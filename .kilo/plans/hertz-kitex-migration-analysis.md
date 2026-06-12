# Hertz / Kitex Migration Analysis — Gosmee

## 1. État des lieux du code existant

| Composant | Technologie actuelle | Rôle |
|---|---|---|
| HTTP server | `net/http` + `chi/v5` (router) + `r3labs/sse/v2` (SSE) | Webhook ingestion, SSE streaming, Admin UI, API |
| Messaging interne | `nats-io/nats-server/v2` (embedded) + `nats.go` (client) | Fan-out temps réel inter-instances |
| Consensus config | `hashicorp/raft` + `go.etcd.io/bbolt` | Réplication config |
| gRPC | **Aucun** | Non utilisé |

Le code HTTP se trouve principalement dans `gosmee/server.go` (~852 lignes) :
- Routeur chi avec `mainRouter`, `restrictedRouter`, `apiRouter`
- Handlers `http.HandlerFunc` standards
- SSE via `http.Flusher` + channels Go
- TLS via `autocert` Let's Encrypt
- Middlewares chi (`RequestID`, `Logger`, `Recoverer`, `ipRestrictMiddleware`, `RequireAuthDynamic`)

Le client (`gosmee/client.go`) utilise `r3labs/sse/v2` comme client SSE standard.

---

## 2. Hertz — Analyse pour le backend HTTP

### 2.1 Avantages

| Avantage | Détail |
|---|---|
| **Performance brute** | Hertz est basé sur `netpoll` (epoll/kqueue), pas de goroutine par connexion. Débit plus élevé, latence plus basse sur très haute charge. |
| **SSE officiel** | Extension `hertz-contrib/sse` native, sans librairie tierce comme `r3labs/sse`. Moins de dépendances externes. |
| **HTTP/2 & HTTP/3** | Support natif, le projet pourrait bénéficier de HTTP/2 push/multiplexing et QUIC. |
| **Middleware intégrés** | CORS, rate limiting, circuit breaker, tracing OpenTelemetry, etc. déjà disponibles dans l'écosystème CloudWeGo. |
| **Séparation codec/transport** | Hertz sépare le protocole du codec, facilitant l'ajout de protocoles alternatifs (Protobuf, Thrift, etc.). |
| **Validateurs de binding** | Validation automatique des structs avec tags `go-tagexpr`, plus performant que `json.Unmarshal` manuel. |

### 2.2 Inconvénients

| Inconvénient | Détail |
|---|---|
| **Migration lourde** | Tous les handlers `func(w http.ResponseWriter, r *http.Request)` doivent être réécrits en `func(c context.Context, ctx *app.RequestContext)`. |
| **Incompatibilités `net/http`** | Hertz est basé sur `fasthttp`, pas sur `net/http`. Impossible de mixer des middlewares chi ou stdlib. Tout le code touchant `http.ResponseWriter` / `*http.Request` doit être adapté. |
| **SSE client incompatible** | Le client utilise `r3labs/sse/v2` qui attend un endpoint SSE standard. Si le serveur passe à `hertz-contrib/sse`, le format peut être légèrement différent. À vérifier. |
| **`autocert` incompatible** | Le gestionnaire TLS Let's Encrypt (`golang.org/x/crypto/acme/autocert`) n'est pas compatible avec les listeners `netpoll`. Il faudrait utiliser `hertz-contrib/autocert` ou un reverse proxy (Caddy/Nginx). |
| **Dépendances supplémentaires** | Hertz + ses contribs ajoutent ~50-100 dépendances Go (hertz, fasthttp, netpoll, bytedance/sonic, etc.). |
| **Maturité moindre** | Hertz est plus jeune que `net/http`. Moins de stackoverflow, moins de tooling communautaire. |
| **Embarqué NATS** | Le serveur NATS utilise `net.Listener` standard. Hertz ayant son propre système de listener, la cohabitation peut être complexe si on veut tout sur le même process. |
| **Problème du `gitops`** | Avec des entreprises qui débloquent les tags et domaines CloudWeGo/ByteDance, certains firewall d'entreprise peuvent bloquer `go mod download`. |

### 2.3 Verdict Hertz

Le projet actuel est un serveur webhook simple avec SSE. La couche HTTP est fine, le vrai travail est dans NATS et Raft. **Le rapport coût/bénéfice est défavorable** pour une migration complète vers Hertz :

- Migrer 852 lignes de handlers + middlewares
- Remplacer le client SSE (`r3labs/sse/v2`)
- Gérer l'incompatibilité autocert
- Tester toute la stack end-to-end
- Pour un gain de performance qui n'est pas le bottleneck (le bottleneck est NATS/Raft, pas HTTP)

---

## 3. Kitex — Analyse pour gRPC

### 3.1 État actuel

**Le projet n'utilise pas gRPC.** Aucun fichier `.proto`, aucun import gRPC.

La communication inter-process se fait via :
- **Raft** (TCP port 6001) : réplication de configuration
- **NATS** (TCP port 6222) : fan-out de webhooks

### 3.2 Si gRPC était introduit (nouveau, pas remplacement)

Scénarios où gRPC pourrait avoir du sens :
- API Admin au lieu de l'API REST actuelle (`/api/*` routes)
- Communication entre le serveur gosmee et un service externe de traitement

Mais pour le cas d'usage actuel (webhook relay), **gRPC n'apporte rien que NATS ne fait déjà mieux** :
- NATS est plus léger pour du fan-out (pas de streaming bidirectionnel lourd)
- NATS a une latence plus basse que gRPC pour du pub/sub
- Le client gosmee utilise SSE, pas gRPC

### 3.3 Verdict Kitex

Kitex n'est **pas pertinent** pour ce projet. Si un jour une API gRPC est nécessaire, Kitex serait un bon candidat, mais aujourd'hui c'est du YAGNI.

---

## 4. Recommandation

**Ne pas migrer.** Le stack actuel (`net/http` + `chi` + `r3labs/sse` + `nats-server`) est :

1. **Éprouvé** — des milliers de déploiements Go utilisent cette combinaison
2. **Simple** — le code est lisible, sans couche d'abstraction superflue
3. **Suffisant** — les performances sont limitées par NATS/Raft, pas par HTTP
4. **Zéro dépendance supplémentaire** — pas de risque de blocage réseau d'entreprise
5. **Fonctionnel** — SSE marche, la validation marche, le TLS marche

---

## 5. Plan d'implémentation (si migration décidée malgré tout)

Si après analyse la décision est de migrer, voici le plan par étapes pour un LLM Flash :

### Phase 1 — Préparation (structures et dépendances)

**Fichiers à créer/modifier :**

1. `go.mod` : ajouter les dépendances Hertz
   ```
   github.com/cloudwego/hertz v0.9.x
   github.com/hertz-contrib/sse v0.1.x
   github.com/hertz-contrib/cors v0.1.x
   github.com/hertz-contrib/requestid v0.1.x
   github.com/hertz-contrib/logger v0.1.x
   ```

2. Créer `gosmee/hertz_types.go` :
   - Définir les structs de binding pour les requêtes POST, replay, API
   - Remplacer `json.Unmarshal` par des struct tags `form`/`json` + binding hertz
   - Définir les structs de réponse avec tags `json`

3. Créer `gosmee/hertz_context.go` :
   - Wrapper pour `app.RequestContext` → accès aux interfaces RaftStore, EventBroker, NATS Broker
   - Fonctions helper : `getChannel(ctx)`, `getPublicURL(ctx)`, `writeJSON(ctx, data)`

### Phase 2 — Migration des middlewares

**Fichier :** `gosmee/hertz_middleware.go`

Adapter chaque middleware chi en middleware Hertz :

| Middleware actuel (chi) | Cible Hertz |
|---|---|
| `middleware.RequestID` | `hertz-contrib/requestid` intégré |
| `middleware.Logger` | Handler hertz avec `hlog` |
| `middleware.Recoverer` | `recovery` intégré dans hertz |
| `ipRestrictMiddleware(rs)` | Réécrire avec `app.RequestContext.RemoteAddr()` |
| `RequireAuthDynamic(rs)` | Réécrire avec `ctx.GetBool("authenticated")` |

### Phase 3 — Migration des handlers

**Fichier :** `gosmee/hertz_handlers.go`

| Handler actuel | Signature cible |
|---|---|
| `handleWebhookPost(...)` | `func(c context.Context, ctx *app.RequestContext)` |
| `handleEventsGet(...)` | `func(c context.Context, ctx *app.RequestContext)` |
| `handleReplayPost(...)` | `func(c context.Context, ctx *app.RequestContext)` |
| `retVersion(...)` | `func(c context.Context, ctx *app.RequestContext)` |
| `LoginHandlerDynamic(...)` | `func(c context.Context, ctx *app.RequestContext)` |
| Handlers d'API (`store.APIHandlers`) | Chaque handler adapté individuellement |

**Exemple de transformation :**

```go
// AVANT (chi + net/http)
func retVersion(w http.ResponseWriter, _ *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{"version": ...})
}

// APRÈS (hertz)
func retVersionHertz(c context.Context, ctx *app.RequestContext) {
    ctx.JSON(200, map[string]string{"version": Version})
}
```

### Phase 4 — Migration SSE

**Fichier :** `gosmee/hertz_sse.go`

1. Remplacer `sseLoop(w, flusher, ...)` par un stream hertz SSE :
   ```go
   s := sse.NewStream(c)
   for {
       select {
       case data := <-events:
           s.Publish(&sse.Event{Data: data})
       case <-ticker.C:
           s.Publish(&sse.Event{Comment: "keepalive"})
       }
   }
   ```
2. Adapter le `Broker.Subscribe()` / `EventBroker.Subscribe()` pour retourner le même `chan []byte`
3. Gérer le `clientGone` via `ctx.Done()`

**⚠️ Note :** Le client SSE reste inchangé (`r3labs/sse/v2`). Le serveur doit émettre un format SSE compatible. À tester.

### Phase 5 — Routeur et serveur

**Fichier :** `gosmee/hertz_server.go` (remplace la fonction `serve` dans `server.go`)

```go
func serveHertz(c *cli.Context) error {
    // ... init Raft, NATS, EventBroker (inchangé) ...
    
    h := server.Default(
        server.WithHostPorts(portAddr),
        server.WithTLS(certFile, certKey),
    )
    
    // Routes non protégées
    h.GET("/version", retVersionHertz)
    h.GET("/health", retVersionHertz)
    h.GET("/events/:channel", handleEventsGetHertz(broker, rs))
    
    // Routes POST (avec middleware IP)
    postGroup := h.Group("", ipRestrictHertz(rs))
    postGroup.POST("/:channel", handleWebhookPostHertz(events, broker, rs))
    postGroup.POST("/replay/:channel", handleReplayPostHertz(events, broker, rs))
    
    // API
    apiGroup := h.Group("/api", RequireAuthDynamicHertz(rs))
    apiGroup.GET("/*path", apiHandlerHertz(rs))
    
    h.Spin()
}
```

**Suppression :** L'ancien `server.go` devient obsolète (ou gardé derrière un flag `--http-framework=chi` pour rétrocompatibilité).

### Phase 6 — Nettoyage

1. Supprimer les imports `chi`, `r3labs/sse/v2` du serveur (pas du client)
2. Supprimer `EventBroker` et `NewEventBroker` (remplacé par le Broker NATS ou le SSE hertz)
3. Mettre à jour les tests `server_test.go` → `hertz_server_test.go`
4. `go mod tidy` pour nettoyer les dépendances inutilisées

### Phase 7 — Tests et validation

1. Tests unitaires : chaque handler hertz (utiliser `app.RequestContext` + `httptest` compatible hertz)
2. Tests d'intégration : client SSE existant contre le nouveau serveur
3. Test de charge : comparer les métriques avant/après
4. Test de compatibilité : le client CLI (`gosmee client`) doit fonctionner sans changement

---

## 6. Résumé exécutif

| Critère | Rester sur chi/net/http | Migrer vers Hertz |
|---|---|---|
| Effort | 0 | ~5-8 jours de dev + tests |
| Risque | Aucun | Compatibilité SSE client, autocert, NATS cohabitation |
| Bénéfice | Stabilité | ~10-30% débit HTTP max (pas le bottleneck) |
| Maintenance | Faible | Plus de dépendances à suivre |
| Dépendances | 23 (go mod) | ~70+ après migration |

**Recommandation finale :** Ne pas migrer. Le stack actuel est optimal pour le cas d'usage. Si toutefois la décision est prise de migrer, suivre le plan Phase 1→7 ci-dessus.
