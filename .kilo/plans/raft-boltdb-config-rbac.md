# Plan: Raft + BoltDB — Configuration Dynamique, Projets & RBAC/ACL

## Objectif

Remplacer la configuration fichier statique par un système distribué basé sur **Hashicorp Raft + BoltDB** pour :
1. **Configuration dynamique** des projets (ex-channels) — modifiable via UI sans restart
2. **Configuration globale** et valeurs par défaut — centralisées dans Raft
3. **RBAC / ACL** — contrôle d'accès fin sur les projets et la configuration globale (rôle `admin`)
4. **Bootstrap** au déploiement — fichier YAML lu au premier démarrage

Toute la configuration qui était dans des fichiers (auth-config, encrypted-channels, etc.) est migrée dans Raft. Seule la configuration du cluster Raft lui-même reste en flags/env.

---

## Architecture

```
┌──────────────────────────────────────────────────────┐
│                  gosmee server                        │
│                                                       │
│  ┌──────────────┐   ┌──────────────┐   ┌───────────┐ │
│  │   HTTP API   │   │  Raft FSM    │   │  BoltDB   │ │
│  │  (chi router)│◄──┤  (State      │◄──┤  (stable  │ │
│  │              │   │   Machine)   │   │   store)  │ │
│  │  GET/POST    │   │              │   │           │ │
│  │  /api/*      │   │  snapshots   │   │  logs     │ │
│  └──────────────┘   └──────┬───────┘   └───────────┘ │
│                             │                         │
│                    ┌────────▼────────┐                │
│                    │   Raft Layer    │                │
│                    │ (hashicorp/raft)│                │
│                    │                 │                │
│                    │ TCP transport   │──► pairs       │
│                    └─────────────────┘                │
└──────────────────────────────────────────────────────┘
```

- **1 noeud par défaut** — démarre comme leader unique, pas de quorum nécessaire
- **3+ noeuds optionnel** — via `--raft-peers` pour HA, les flags Raft restent seuls en fichier/env
- **BoltDB** est le `LogStore` + `StableStore` + stockage des snapshots FSM
- **Fichier bootstrap** (`bootstrap.yaml`) ingéré au premier démarrage


## Modèle de données (FSM)

Le FSM est un key-value store hiérarchique stocké dans BoltDB via un bucket tree
(`/` est un séparateur logique). Toutes les opérations passent par `raft.Apply()`.

```
/global/
  ├── server/
  │   ├── max_body_size        = <int>      (défaut: 26214400)
  │   ├── trust_proxy          = <bool>     (défaut: false)
  │   ├── cors_origin          = <string>   (défaut: "*")
  │   └── footer               = <string>   (HTML footer UI)
  │
  └── defaults/
      └── project/
          ├── webhook_signatures = <json []string>
          ├── allowed_ips        = <json []string>
          └── replay_token_hash  = <sha256 string>

/projects/<project_id>/
  ├── name                     = <string>
  ├── webhook_signatures       = <json []string>   (override ou vide = global)
  ├── allowed_ips              = <json []string>   (override ou vide = global)
  ├── max_body_size            = <int>              (0 = inherit global)
  ├── replay_token             = <sha256 string>    (hash du token, vide = désactivé)
  ├── encryption_enabled       = <bool>
  ├── encryption_public_keys   = <json []string
  └── hooks/
      └── <hook_id>/
          ├── name             = <string>
          └── config           = <json>

/rbac/
  ├── roles/
  │   ├── admin/
  │   │   └── permissions      = <json []string>  (ex: ["*"])
  │   ├── project_admin/
  │   │   └── permissions      = <json []string>  (ex: ["project:write", "project:read"])
  │   └── project_viewer/
  │       └── permissions      = <json []string>  (ex: ["project:read"])
  │
  └── bindings/
      └── <user_id>/
          ├── roles            = <json []string>  (ex: ["admin"])
          └── projects         = <json []string>  (ex: ["*"] ou ["proj1","proj2"])

/users/
  └── <user_id>/
      ├── username             = <string>
      ├── password_hash        = <bcrypt string>
      └── oidc_subjects        = <json []string>  (optionnel, pour mapping OIDC)
```

**Permissions prédéfinies :**

| Permission           | Description                          |
|----------------------|--------------------------------------|
| `*`                  | Tout (admin)                         |
| `global:read`        | Lire la configuration globale        |
| `global:write`       | Modifier la configuration globale    |
| `users:read`         | Lister/voir les utilisateurs         |
| `users:write`        | Créer/modifier/supprimer utilisateurs|
| `rbac:read`          | Lire la configuration RBAC           |
| `rbac:write`         | Modifier la configuration RBAC       |
| `project:read`       | Lire les configurations de projet    |
| `project:write`      | Modifier/Créer/Supprimer des projets |
| `project:view`       | Voir les événements SSE d'un projet  |

**Héritage projet / global :**
- Si un champ projet n'est pas défini (vide/zéro), le serveur lit `/global/defaults/project/`
- Si le global default n'est pas défini non plus, utilise les valeurs hardcodées (celles d'aujourd'hui dans `flags.go`)


## Bootstrap au déploiement

Format `bootstrap.yaml` :

```yaml
# bootstrap.yaml — lu UNIQUEMENT au premier démarrage (FSM vide)
# Chemin: --bootstrap-config-file ou GOSMEE_BOOTSTRAP_CONFIG

raft:
  # Optionnel: config du cluster si multi-noeuds
  # node_id: "node1"
  # peers: ["node2:6001", "node3:6001"]

global:
  server:
    max_body_size: 26214400
    trust_proxy: false
    cors_origin: "*"
    footer: "Contact: <a href='mailto:admin@example.com'>Admin</a>"
  defaults:
    project:
      webhook_signatures: []
      allowed_ips: []
      replay_token: ""

users:
  - username: "admin"
    password: "changeme"  # sera haché en bcrypt à l'ingestion
    roles: ["admin"]
    projects: ["*"]

projects:
  - id: "my-first-project"
    name: "My First Project"
    webhook_signatures: ["secret123"]
    allowed_ips: ["10.0.0.0/8"]
```

Le fichier est lu par `LoadBootstrap(path)` :
1. Parse YAML/JSON
2. Applique séquentiellement via `raft.Apply()` :
   - Global config
   - Users (hash des passwords en bcrypt)
   - RBAC bindings
   - Projets
3. Log chaque étape
4. Si échec partiel → rollback ? Non : le FSM est vide, un échec signifie qu'on arrête le serveur avec une erreur claire.

Après ce premier bootstrap, le fichier est ignoré en redémarrage (FSM non vide).


## Fichiers à créer

### 1. `gosmee/store/` — package de stockage Raft

```
gosmee/store/
├── fsm.go              # FSM implementation (raft.FSM interface)
├── fsm_test.go
├── raft.go             # Raft cluster setup, bootstrap, leadership
├── raft_test.go
├── bolt.go             # BoltDB log/stable store implementation
├── bolt_test.go
├── types.go            # Types du data model (projets, users, RBAC, etc.)
├── api.go              # Handlers HTTP pour l'API de gestion
├── api_test.go
├── bootstrap.go        # Chargement du fichier bootstrap
├── bootstrap_test.go
└── acl.go              # Middleware RBAC + helpers de vérification
```

### 2. `gosmee/flags.go` — nouveaux flags serveur

Ajouter à `serverFlags` :
```go
&cli.StringFlag{
    Name:    "raft-dir",
    Usage:   "Raft data directory (BoltDB stores)",
    Value:   "./raft-data",
    EnvVars: []string{"GOSMEE_RAFT_DIR"},
},
&cli.StringFlag{
    Name:    "raft-node-id",
    Usage:   "Unique Raft node ID",
    Value:   "node1",
    EnvVars: []string{"GOSMEE_RAFT_NODE_ID"},
},
&cli.StringFlag{
    Name:    "raft-bind-addr",
    Usage:   "Raft TCP bind address for inter-node communication",
    Value:   "127.0.0.1:6001",
    EnvVars: []string{"GOSMEE_RAFT_BIND_ADDR"},
},
&cli.StringSliceFlag{
    Name:    "raft-peers",
    Usage:   "Other Raft node IDs and addresses (node2=addr:port,node3=addr:port). Not needed for single-node",
    EnvVars: []string{"GOSMEE_RAFT_PEERS"},
},
&cli.StringFlag{
    Name:    "bootstrap-config-file",
    Usage:   "Path to bootstrap YAML/JSON config file (read once when FSM is empty)",
    EnvVars: []string{"GOSMEE_BOOTSTRAP_CONFIG_FILE"},
},
```

### 3. `gosmee/templates/admin.tmpl` — UI d'administration
Page SPA légère (vanilla JS) servie à `GET /admin` qui interagit avec l'API REST.

### 4. `gosmee/store/fsm.go` — State Machine

```go
type FSM struct {
    db *bolt.DB // BoltDB connection
}

// Implémente raft.FSM:
func (f *FSM) Apply(log *raft.Log) interface{}
func (f *FSM) Snapshot() (raft.FSMSnapshot, error)
func (f *FSM) Restore(rc io.ReadCloser) error

// Commandes internes au FSM:
type fsmCommand struct {
    Op    string // "set", "delete", "set-json"
    Key   string
    Value []byte
}

func (f *FSM) applySet(tx *bolt.Tx, parts []string, value []byte) error
func (f *FSM) applyDelete(tx *bolt.Tx, parts []string) error
```

Le FSM marshalle les buckets BoltDB comme snapshot (dump complet de toutes les clés).

### 5. `gosmee/store/types.go` — Types

```go
type Project struct {
    ID                 string   `json:"id"`
    Name               string   `json:"name"`
    WebhookSignatures  []string `json:"webhook_signatures,omitempty"`
    AllowedIPs         []string `json:"allowed_ips,omitempty"`
    MaxBodySize        int      `json:"max_body_size,omitempty"`
    ReplayToken        string   `json:"replay_token,omitempty"`      // stocké hashé
    EncryptionEnabled  bool     `json:"encryption_enabled,omitempty"`
    EncryptionPubKeys  []string `json:"encryption_public_keys,omitempty"`
}

type GlobalConfig struct {
    Server   ServerConfig              `json:"server"`
    Defaults DefaultProjectConfig      `json:"defaults"`
}

type ServerConfig struct {
    MaxBodySize int    `json:"max_body_size"`
    TrustProxy  bool   `json:"trust_proxy"`
    CORSOrigin  string `json:"cors_origin"`
    Footer      string `json:"footer"`
}

type DefaultProjectConfig struct {
    WebhookSignatures []string `json:"webhook_signatures"`
    AllowedIPs        []string `json:"allowed_ips"`
    ReplayToken       string   `json:"replay_token"`
}

type User struct {
    ID           string   `json:"id"`
    Username     string   `json:"username"`
    PasswordHash string   `json:"password_hash,omitempty"`
    OIDCSubjects []string `json:"oidc_subjects,omitempty"`
    Roles        []string `json:"roles"`
    Projects     []string `json:"projects"`
}

type Role struct {
    Name        string   `json:"name"`
    Permissions []string `json:"permissions"`
}

type Permission string

const (
    PermAll             Permission = "*"
    PermGlobalRead      Permission = "global:read"
    PermGlobalWrite     Permission = "global:write"
    PermUsersRead       Permission = "users:read"
    PermUsersWrite      Permission = "users:write"
    PermRBACRead        Permission = "rbac:read"
    PermRBACWrite       Permission = "rbac:write"
    PermProjectRead     Permission = "project:read"
    PermProjectWrite    Permission = "project:write"
    PermProjectView     Permission = "project:view"
)
```

### 6. `gosmee/store/raft.go` — Setup cluster Raft

```go
type RaftStore struct {
    raft  *raft.Raft
    fsm   *FSM
    db    *bolt.DB
}

func NewRaftStore(dir, nodeID, bindAddr string, peers []string, bootstrapPath string) (*RaftStore, error)
func (rs *RaftStore) IsLeader() bool
func (rs *RaftStore) WaitForLeader(timeout time.Duration) error
func (rs *RaftStore) Apply(cmd []byte) (interface{}, error)
func (rs *RaftStore) Shutdown() error

// Helpers de haut niveau :
func (rs *RaftStore) GetProject(id string) (*Project, error)
func (rs *RaftStore) ListProjects() ([]*Project, error)
func (rs *RaftStore) CreateProject(p *Project) error
func (rs *RaftStore) UpdateProject(p *Project) error
func (rs *RaftStore) DeleteProject(id string) error
func (rs *RaftStore) GetGlobalConfig() (*GlobalConfig, error)
func (rs *RaftStore) UpdateGlobalConfig(cfg *GlobalConfig) error
func (rs *RaftStore) GetUser(id string) (*User, error)
func (rs *RaftStore) ListUsers() ([]*User, error)
func (rs *RaftStore) CreateUser(u *User) error
func (rs *RaftStore) UpdateUser(u *User) error
func (rs *RaftStore) DeleteUser(id string) error
func (rs *RaftStore) HasData() bool // true si le FSM contient déjà des données (évite double bootstrap)
```

### 7. `gosmee/store/acl.go` — RBAC Middleware

```go
func RequirePermission(rs *RaftStore, perm Permission) func(http.Handler) http.Handler
func UserHasPermission(rs *RaftStore, username string, perm Permission, projectID string) bool
func UserProjects(rs *RaftStore, username string) ([]string, error)
```

Le username est extrait du cookie de session (le `RequireAuth` existant le met dans le context). Le middleware RBAC :
1. Récupère le username du context
2. Récupère l'utilisateur depuis le FSM
3. Récupère ses roles et bindings
4. Vérifie la permission demandée (avec scope projet si applicable)

### 8. `gosmee/store/api.go` — API REST

```go
func RegisterAPIHandlers(r chi.Router, rs *RaftStore)
```

Endpoints :

| Méthode | Path | Permission | Description |
|---------|------|-----------|-------------|
| GET | `/api/projects` | `project:read` | Liste projets (filtré par scope) |
| POST | `/api/projects` | `project:write` | Crée un projet |
| GET | `/api/projects/{id}` | `project:read` | Détail projet |
| PUT | `/api/projects/{id}` | `project:write` | Modifie projet |
| DELETE | `/api/projects/{id}` | `project:write` | Supprime projet |
| GET | `/api/global` | `global:read` | Lit config globale |
| PUT | `/api/global` | `global:write` | Modifie config globale |
| GET | `/api/users` | `users:read` | Liste utilisateurs |
| POST | `/api/users` | `users:write` | Crée utilisateur |
| PUT | `/api/users/{id}` | `users:write` | Modifie utilisateur |
| DELETE | `/api/users/{id}` | `users:write` | Supprime utilisateur |
| GET | `/api/rbac/roles` | `rbac:read` | Liste roles |
| GET | `/api/rbac/bindings` | `rbac:read` | Liste bindings |
| PUT | `/api/rbac/bindings/{userID}` | `rbac:write` | Modifie binding utilisateur |
| GET | `/api/me` | *(tout user authentifié)* | Infos de l'utilisateur courant |

Tous ces endpoints sont protégés par `RequireAuth` + `RequirePermission`.

### 9. `gosmee/store/bootstrap.go` — Bootstrap

```go
type BootstrapConfig struct {
    Raft     *BootstrapRaft     `yaml:"raft,omitempty" json:"raft,omitempty"`
    Global   *GlobalConfig      `yaml:"global,omitempty" json:"global,omitempty"`
    Users    []BootstrapUser    `yaml:"users,omitempty" json:"users,omitempty"`
    Projects []BootstrapProject `yaml:"projects,omitempty" json:"projects,omitempty"`
}

type BootstrapUser struct {
    Username string   `yaml:"username" json:"username"`
    Password string   `yaml:"password" json:"password"`
    Roles    []string `yaml:"roles" json:"roles"`
    Projects []string `yaml:"projects" json:"projects"`
}

type BootstrapProject struct {
    ID                string   `yaml:"id" json:"id"`
    Name              string   `yaml:"name" json:"name"`
    WebhookSignatures []string `yaml:"webhook_signatures,omitempty" json:"webhook_signatures,omitempty"`
    AllowedIPs        []string `yaml:"allowed_ips,omitempty" json:"allowed_ips,omitempty"`
}

func LoadBootstrap(path string) (*BootstrapConfig, error)
func (rs *RaftStore) ApplyBootstrap(cfg *BootstrapConfig) error
```

### 10. `gosmee/templates/admin.tmpl` — UI Admin

Page d'administration avec :
- Vue "Projets" : liste, création, édition, suppression
- Vue "Configuration Globale" : formulaire d'édition
- Vue "Utilisateurs" : liste, création, édition, suppression
- Vue "RBAC" : bindings par utilisateur

Tout en vanilla JS + CSS minimal, pas de framework.


## Fichiers à modifier

### 11. `gosmee/server.go` — Intégration Raft

Dans `serve()` :
1. Après avoir parsé les flags auth actuels → initialiser `RaftStore`
2. Si FSM vide + `--bootstrap-config-file` non vide → appliquer le bootstrap
3. Si FSM vide et pas de bootstrap : le serveur démarre quand même, il faut configurer via API (mode vierge)
4. Modifier les handlers existants pour utiliser les configs du FSM au lieu des flags :
   - `handleWebhookPost` : lit `webhook_signatures` du projet puis fallback global
   - `handleReplayPost` : lit `replay_token` (hashé) du projet puis fallback global
   - `handleEventsGet` : lit `encryption_enabled` + `encryption_public_keys` du projet
   - `ipRestrictMiddleware` : lit `allowed_ips` du projet puis fallback global
   - `serveIndex` : lit le footer et les configs UI du projet
   - CORS : lit `cors_origin` du global
   - Max body size : lit `max_body_size` du projet puis fallback global
5. Monter les routes API : `POST /api/*` → `RequireAuth` → `RequirePermission(perm)` → handler
6. Servir `/admin` (protégé par auth)

**Principe clé :** chaque handler de webhook/replay/SSE lit maintenant sa config depuis le FSM via `RaftStore` au moment de la requête (ou au démarrage avec un cache rafraîchi périodiquement).

### 12. `gosmee/flags.go` — Nettoyage

**Supprimer** de `serverFlags` (migrés vers Raft) :
- `--webhook-signature` → devient config projet/global
- `--replay-token` → devient config projet/global
- `--max-body-size` → devient config projet/global
- `--cors-origin` → devient config global
- `--trust-proxy` → devient config global
- `--footer` / `--footer-file` → devient config global
- `--encrypted-channels-file` → devient config projet
- `--auth-config-file` → devient config utilisateurs/RBAC dans FSM
- `--auth-session-secret` → devient config global (hashé)
- `--allowed-ips` → devient config projet/global

**Garder** (spécifiques à l'infrastructure, pas de la config métier) :
- `--port`, `--address`
- `--public-url`
- `--tls-cert`, `--tls-key`
- `--auto-cert`

### 13. `gosmee/auth.go` — Auth migrée vers FSM

- `LoginHandler` : au lieu de lire `auth-config-file`, interroge le `RaftStore` pour trouver l'utilisateur
- `RequireAuth` : ne change pas (reste basé sur le cookie session)
- Le secret session n'est plus stocké dans `auth-config-file` mais dans `/global/server/session_secret` (hashé dans le FSM)
- Pour le OIDC : les providers sont stockés dans `/global/auth/oidc_providers`

### 14. `go.mod` — Nouvelles dépendances

```
github.com/hashicorp/raft
github.com/hashicorp/raft-boltdb/v2
gopkg.in/yaml.v3  (pour le bootstrap YAML — déjà indirect via golangci-lint, ou on utilise encoding/json avec un helper)
```

Note : `raft-boltdb` de Hashicorp est déprécié au profit de `raft-boltdb/v2`. On utilise v2.

### 15. `gosmee/store/bolt.go` — BoltDB stores

Deux buckets dans le même fichier BoltDB :
- `logs` : pour `raft-boltdb/v2` LogStore
- `stable` : pour `raft-boltdb/v2` StableStore  
- Le FSM lui-même utilise des buckets séparés pour sa hiérarchie

On peut réutiliser un seul fichier BoltDB pour tout (logs, stable, FSM data) en utilisant des buckets distincts.

### 16. `gosmee/protected_channels.go` — Modification

`LoadProtectedChannels` n'est plus appelé depuis un fichier mais depuis le `RaftStore`.
On garde la structure `ProtectedChannels` mais on ajoute un constructeur :
```go
func LoadProtectedChannelsFromStore(rs *RaftStore) (*ProtectedChannels, error)
```
Qui interroge tous les projets et construit la map `channel → allowed_public_keys`.

### 17. `gosmee/auth_config.go` — Modification

`LoadAuthConfig` est remplacé par une lecture depuis le FSM. On garde les types (`AuthConfig`, `OIDCConfig`, etc.) mais on change la source.

---

## Ordre d'implémentation

1. **`gosmee/store/types.go`** — Types du data model
2. **`gosmee/store/bolt.go`** — BoltDB setups (log, stable, FSM data)
3. **`gosmee/store/fsm.go`** — FSM (Apply, Snapshot, Restore)
4. **`gosmee/store/raft.go`** — Setup cluster Raft, helpers haut niveau
5. **`gosmee/store/bootstrap.go`** — Chargement + application du bootstrap
6. **`gosmee/store/acl.go`** — Middleware RBAC + helpers permissions
7. **`gosmee/store/api.go`** — Handlers API REST
8. **`gosmee/flags.go`** — Ajout flags Raft, suppression flags fichier
9. **`gosmee/server.go`** — Intégration RaftStore dans `serve()`, migration des handlers
10. **`gosmee/auth.go` + `auth_config.go`** — Migration auth vers FSM
11. **`gosmee/protected_channels.go`** — Migration source vers RaftStore
12. **`gosmee/templates/admin.tmpl`** — UI d'administration
13. **Tests** — `*_test.go` pour chaque fichier du package `store/`, tests d'intégration serveur

---

## Impact sur l'existant

| Ancien système | Nouveau système | Impact |
|---|---|---|
| `--webhook-signature` (flag serveur) | `/projects/<id>/webhook_signatures` | Configurable par projet |
| `--allowed-ips` (flag serveur) | `/projects/<id>/allowed_ips` | Configurable par projet |
| `--max-body-size` (flag serveur) | `/global/server/max_body_size` | Modifiable à chaud |
| `--replay-token` (flag serveur) | `/projects/<id>/replay_token` | Configurable par projet |
| `--cors-origin` (flag serveur) | `/global/server/cors_origin` | Modifiable à chaud |
| `--trust-proxy` (flag serveur) | `/global/server/trust_proxy` | Modifiable à chaud |
| `--footer` / `--footer-file` | `/global/server/footer` | Modifiable à chaud |
| `--encrypted-channels-file` | `/projects/<id>/encryption_*` | Par projet, UI |
| `--auth-config-file` | `/users/*`, `/rbac/*` | RBAC + UI |
| `--auth-session-secret` | `/global/server/session_secret` | Hashé dans FSM |

**Rétrocompatibilité :** les flags supprimés ne seront plus supportés. La migration consiste à fournir un fichier bootstrap au premier démarrage. Si aucun bootstrap n'est fourni, le système démarre "vierge" (pas de projet, pas d'utilisateur) — un message clair est affiché indiquant qu'il faut configurer via `bootstrap.yaml` ou via l'API (si l'API est accessible sans auth initialement, ce qui est le cas : seul un flag `--bootstrap-config-file` permet de créer le premier admin).

**Mode bootstrap initial sans fichier :** si le FSM est vide et aucun bootstrap n'est fourni, le serveur démarre en mode "setup" : les routes API d'admin sont accessibles sans auth pendant les 5 premières minutes (time window) pour permettre la création du premier utilisateur admin via l'API. Une fois qu'un utilisateur est créé, le mode setup se désactive.

---

## Plan de test — Couverture exhaustive

Tous les tests utilisent `gotest.tools/v3/assert`, `testing.T` standard Go, `httptest` et `t.TempDir()` (mêmes patterns que les tests existants).

---

### `gosmee/store/boltdb_test.go` — BoltDB stores

| Test | Ce qui est vérifié |
|------|-------------------|
| `TestNewBoltStore` | Création d'un fichier BoltDB avec les buckets `logs`, `stable` ; le fichier est bien créé sur disque ; les buckets sont accessibles en écriture puis lecture |
| `TestBoltLogStore_FirstIndex` | Sur un store vide, `FirstIndex()` retourne `0` |
| `TestBoltLogStore_LastIndex` | Sur un store vide, `LastIndex()` retourne `0` |
| `TestBoltLogStore_StoreGetLog` | Écriture d'un `raft.Log` (Index=1, Term=1, Data=...) puis `GetLog(1)` retourne le même log ; type, data, index, term identiques |
| `TestBoltLogStore_StoreLogs` | Écriture batch de 10 logs via `StoreLogs()` ; `FirstIndex()` = 1, `LastIndex()` = 10 ; tous les logs sont récupérables individuellement |
| `TestBoltLogStore_DeleteRange` | Après écriture de logs 1-10, `DeleteRange(1, 5)` supprime les logs 1-5 ; `FirstIndex()` = 6, `LastIndex()` = 10 ; l'appel à `GetLog(3)` retourne `raft.ErrLogNotFound` |
| `TestBoltStableStore_SetGet` | `Set(key, val)` puis `Get(key)` retourne la valeur exacte ; `Get("nonexistent")` retourne `ErrKeyNotFound` |
| `TestBoltStableStore_SetGetUint64` | `SetUint64` sur `currentTerm` et `lastVotedFor` ; `GetUint64` retourne les bonnes valeurs |
| `TestBoltLogStore_ConcurrentAccess` | 10 goroutines écrivent des logs simultanément ; aucune corruption, tous les logs sont lisibles |

---

### `gosmee/store/fsm_test.go` — FSM (State Machine)

| Test | Ce qui est vérifié |
|------|-------------------|
| `TestFSM_ApplySet` | `Apply` avec `Op: "set"`, `Key: "/global/server/max_body_size"`, `Value: 42` → relire le FSM confirme la valeur |
| `TestFSM_ApplySetJSON` | `Op: "set-json"` sur `Key: "/projects/test/webhook_signatures"` avec `["s1","s2"]` → relire retourne le JSON sérialisé correct |
| `TestFSM_ApplyDelete` | Set puis `Op: "delete"` → relire retourne `nil` et pas d'erreur |
| `TestFSM_ApplyOnReadOnlyTx` | Vérifie que `Apply` sur un FSM non-leader (tx lecture seule) échoue proprement sans panic |
| `TestFSM_Snapshot` | Set 3 clés → `Snapshot()` → `Persist()` écrit dans un buffer ; le buffer contient les 3 clés |
| `TestFSM_Restore` | Sérialise 3 clés dans un buffer → crée un nouveau FSM → `Restore(buffer)` → les 3 clés sont lisibles ; le snapshot écrase les données existantes |
| `TestFSM_SnapshotRestore_Roundtrip` | FSM avec 20 clés → snapshot → restore dans un nouveau FSM → les 20 clés sont intactes ; vérifie les types (int, string, json) |
| `TestFSM_Restore_Corrupted` | Passe un buffer corrompu à `Restore` → retourne une erreur, pas de panic |
| `TestFSM_Restore_Empty` | Passe un buffer vide (snapshot d'un FSM vide) → `Restore` nettoie tout, le FSM n'a plus de clés |
| `TestFSM_KeyValidation` | `Apply` avec `Key: "../../etc/passwd"` → rejeté, le FSM refuse les clés avec `..` |
| `TestFSM_IncrementingIndex` | `Apply` 5 fois → l'index interne du FSM augmente de 1 à chaque appel |

---

### `gosmee/store/raft_test.go` — RaftStore haut niveau

| Test | Ce qui est vérifié |
|------|-------------------|
| `TestNewRaftStore_SingleNode` | Crée un `RaftStore` single-node (pas de peers) → le noeud devient leader en <5s ; `IsLeader()` retourne `true` ; `WaitForLeader()` retourne sans erreur |
| `TestRaftStore_Shutdown` | `Shutdown()` → `IsLeader()` panique ou retourne `false` ; le fichier BoltDB est fermé (vérifié en le rouvrant avec bolt.Open) |
| `TestRaftStore_HasData` | Nouveau store → `HasData()` = `false` ; appliquer une config → `HasData()` = `true` ; ré-ouvrir le même dossier → `HasData()` = `true` |
| `TestRaftStore_CreateProject` | `CreateProject(&Project{ID: "p1", Name: "Proj1"})` → `GetProject("p1")` retourne le projet ; deuxième `CreateProject` avec le même ID → erreur |
| `TestRaftStore_UpdateProject` | Create puis `UpdateProject` avec attributs modifiés → `GetProject` reflète les modifs |
| `TestRaftStore_DeleteProject` | Create → Delete → `GetProject` retourne erreur "not found" |
| `TestRaftStore_ListProjects` | 3 projets créés → `ListProjects()` retourne 3 éléments (ordre non déterministe mais tous présents) |
| `TestRaftStore_GetGlobalConfig` | `GetGlobalConfig()` sur un store vierge → retourne les valeurs par défaut (MaxBodySize=26214400, TrustProxy=false, etc.) |
| `TestRaftStore_UpdateGlobalConfig` | Modifie `ServerConfig.MaxBodySize = 42` → `GetGlobalConfig()` retourne la nouvelle valeur |
| `TestRaftStore_CRUD_Users` | Create → Get → Update → Delete → Get (not found) ; `ListUsers` après 2 créations |
| `TestRaftStore_RBAC_Roles` | Définit les rôles prédéfinis (admin, project_admin, project_viewer) → `GetRole("admin")` retourne `["*"]` |
| `TestRaftStore_RBAC_Bindings` | Assigner `admin` role à `user1` → `ListBindings()` inclut le binding ; modifier les projets → binding reflète le changement |
| `TestRaftStore_ProjectConfigFallback` | Projet sans `max_body_size` → `ResolveProjectConfig` retourne le global default |
| `TestRaftStore_ProjectConfigOverride` | Projet avec `max_body_size=100` → `ResolveProjectConfig` retourne `100`, pas le global |
| `TestRaftStore_ReplayTokenHashed` | `SetProjectReplayToken("p1", "my-token")` → le token est stocké hashé (SHA-256) ; `ValidateReplayToken("p1", "my-token")` = `true` ; `ValidateReplayToken("p1", "wrong")` = `false` |
| `TestRaftStore_ConcurrentReads` | 10 goroutines lisent les projets simultanément → aucune race (vérifié avec `-race`) |

---

### `gosmee/store/bootstrap_test.go` — Bootstrap

| Test | Ce qui est vérifié |
|------|-------------------|
| `TestLoadBootstrap_YAML` | Parse un `bootstrap.yaml` complet (global, 1 user, 1 projet) → `BootstrapConfig` struct contient toutes les données |
| `TestLoadBootstrap_JSON` | Même contenu en JSON → parsing correct, identique au YAML |
| `TestLoadBootstrap_InvalidYAML` | Fichier YAML malformé → erreur de parsing explicite |
| `TestLoadBootstrap_EmptyFile` | Fichier vide → `BootstrapConfig` avec tous les champs nil/empty, pas d'erreur |
| `TestLoadBootstrap_FileNotFound` | Chemin inexistant → erreur |
| `TestApplyBootstrap_GlobalConfig` | Applique un bootstrap avec `global.server.max_body_size: 100` sur un FSM vide → le FSM contient la valeur ; `HasData()` = `true` |
| `TestApplyBootstrap_Users_PasswordHashed` | Applique bootstrap avec `users: [{username: admin, password: changeme}]` → le FSM contient `password_hash` bcrypt, pas le password en clair ; `ValidatePassword` fonctionne |
| `TestApplyBootstrap_Users_RBAC` | Applique bootstrap avec un user `roles: [admin]` → `GetUser("admin-xxx")` a bien `Roles: ["admin"]` et `Projects: ["*"]` |
| `TestApplyBootstrap_Projects` | Applique bootstrap avec 2 projets → `ListProjects()` retourne les 2 projets avec tous leurs champs |
| `TestApplyBootstrap_DoubleApplication` | Applique le bootstrap une deuxième fois sur le même FSM → erreur car `HasData()` = `true` ; le FSM n'est pas modifié par la deuxième tentative |
| `TestApplyBootstrap_AllFields` | Bootstrap avec TOUS les champs possibles (webhook_signatures, allowed_ips, encryption_enabled, encryption_public_keys, replay_token, max_body_size, cors_origin, trust_proxy, footer, oidc_providers) → chaque champ est correctement stocké et lisible |
| `TestApplyBootstrap_DefaultValues` | Bootstrap partiel (que `users`) → `GetGlobalConfig()` retourne les valeurs par défaut ; `ListProjects()` est vide ; aucun crash |
| `TestApplyBootstrap_NoUsers` | Bootstrap sans section `users` → pas d'erreur ; le serveur démarrera en mode setup |
| `TestApplyBootstrap_Validation_InvalidProjectID` | Bootstrap avec un projet `id: ""` → erreur "project ID required" |
| `TestApplyBootstrap_Validation_InvalidIP` | Bootstrap avec `allowed_ips: ["not-an-ip"]` → erreur de parsing IP |

---

### `gosmee/store/acl_test.go` — RBAC / ACL

| Test | Ce qui est vérifié |
|------|-------------------|
| `TestUserHasPermission_Admin_Wildcard` | User avec role `admin` (permission `*`) → `UserHasPermission` retourne `true` pour TOUTES les permissions (`global:read`, `project:write`, `rbac:read`, etc.) |
| `TestUserHasPermission_GlobalWrite` | User `project_admin` (permissions `project:write`, `project:read`) → `global:write` = `false` ; `project:write` = `true` |
| `TestUserHasPermission_ProjectScope` | User `project_admin` avec `projects: ["proj1"]` → `project:read` sur `proj1` = `true` ; `project:read` sur `proj2` = `false` |
| `TestUserHasPermission_StarProjects` | User avec `projects: ["*"]` → `project:read` sur n'importe quel projet = `true` |
| `TestUserHasPermission_EmptyProjects` | User avec `projects: []` → `project:read` sur n'importe quel projet = `false` |
| `TestUserHasPermission_UnknownUser` | `UserHasPermission` pour un username inexistant → `false` |
| `TestUserProjects` | User avec `projects: ["p1","p2"]` → `UserProjects(username)` retourne `["p1","p2"]` ; user avec `projects: ["*"]` → `UserProjects` retourne la liste complète des projets du FSM |
| `TestRequirePermission_Middleware_Allowed` | Middleware `RequirePermission(rs, "global:read")` → user admin → le handler next est appelé, status 200 |
| `TestRequirePermission_Middleware_Denied` | Middleware `RequirePermission(rs, "global:write")` → user project_viewer → status 403 Forbidden, le handler next n'est pas appelé |
| `TestRequirePermission_Middleware_NoAuth` | Pas de cookie session → middleware → status 401 Unauthorized |
| `TestRequirePermission_Middleware_ProjectScoped` | Route `PUT /api/projects/{id}` avec middleware `RequirePermission(rs, "project:write")` + paramètre projet → user admin sur `projects: ["p1"]` accède à `p1` = OK ; accède à `p2` = 403 |
| `TestDefaultRoles_DontConflict` | Les 3 rôles prédéfinis sont créés au bootstrap ; tenter de les recréer manuellement retourne une erreur "already exists" |
| `TestGetUser_Permissions` | `GetUser(username)` → les permissions sont calculées correctement à partir des rôles ; pas de duplication |
| `TestIsAdmin` | Helper `IsAdmin(username)` → admin = `true` ; project_admin = `false` |

---

### `gosmee/store/api_test.go` — API REST

Tous les tests démarrent un `RaftStore` single-node avec bootstrap contenant un admin + 2 projets. Chaque test simule un cookie de session admin.

| Test | Ce qui est vérifié |
|------|-------------------|
| `TestAPI_ListProjects` | `GET /api/projects` → 200, JSON array avec 2 projets ; user non-admin scope `proj1` → ne voit que `proj1` |
| `TestAPI_CreateProject` | `POST /api/projects` avec body JSON `{id, name, webhook_signatures}` → 201 Created ; `GET /api/projects/new-id` → 200 avec le projet créé |
| `TestAPI_CreateProject_Duplicate` | `POST /api/projects` avec ID existant → 409 Conflict |
| `TestAPI_CreateProject_InvalidBody` | `POST /api/projects` avec `{foo: bar}` → 400 Bad Request (champ `id` requis) |
| `TestAPI_GetProject` | `GET /api/projects/proj1` → 200, contient tous les champs |
| `TestAPI_GetProject_NotFound` | `GET /api/projects/nonexistent` → 404 |
| `TestAPI_UpdateProject` | `PUT /api/projects/proj1` avec `{name: "Renamed"}` → 200 ; `GET /api/projects/proj1` → `name: "Renamed"` |
| `TestAPI_DeleteProject` | `DELETE /api/projects/proj1` → 204 ; `GET /api/projects/proj1` → 404 |
| `TestAPI_GetGlobalConfig` | `GET /api/global` → 200, retourne le `GlobalConfig` complet |
| `TestAPI_UpdateGlobalConfig` | `PUT /api/global` avec `{server: {max_body_size: 100}}` → 200 ; `GET /api/global` → max_body_size = 100 |
| `TestAPI_ListUsers` | `GET /api/users` → 200, contient l'admin user (password_hash masqué) |
| `TestAPI_CreateUser` | `POST /api/users` avec `{username, password, roles}` → 201 ; le password est hashé bcrypt dans le FSM |
| `TestAPI_UpdateUser` | `PUT /api/users/xxx` → modifie les rôles → 200 |
| `TestAPI_DeleteUser` | `DELETE /api/users/xxx` → 204 ; ne peut pas supprimer le dernier admin |
| `TestAPI_GetRBAC` | `GET /api/rbac/roles` → 200, liste les rôles ; `GET /api/rbac/bindings` → 200, liste les bindings |
| `TestAPI_UpdateRBACBindings` | `PUT /api/rbac/bindings/user1` → met à jour le binding → 200 |
| `TestAPI_GetMe` | `GET /api/me` avec cookie admin → 200, retourne `{username, roles, projects, permissions}` |
| `TestAPI_AllEndpoints_Unauthorized` | Chaque endpoint sans cookie → 302 Found (redirect login) ou 401 |
| `TestAPI_AllEndpoints_Forbidden` | User `project_viewer` → `POST /api/projects` → 403 ; `PUT /api/global` → 403 |
| `TestAPI_SetupMode` | FSM vide, pas de bootstrap → `POST /api/users` sans auth → 201 (setup mode actif) ; après création du 1er user → `POST /api/users` sans auth → 401 (setup mode désactivé) |
| `TestAPI_SetupMode_Timeout` | FSM vide → attendre 5+ minutes → `POST /api/users` sans auth → 401 (fenêtre expirée) |
| `TestAPI_CSV_Export` | `GET /api/projects?format=csv` → 200, Content-Type `text/csv`, lignes = nombre de projets + header |
| `TestAPI_ContentType` | Tous les endpoints renvoient `Content-Type: application/json` sauf `format=csv` |

---

### `gosmee/store/server_integration_test.go` — Intégration serveur

| Test | Ce qui est vérifié |
|------|-------------------|
| `TestServer_StartsWithBootstrap` | Démarre un serveur complet avec `--bootstrap-config-file` → le serveur répond sur `/version` ; les projets du bootstrap sont accessibles via l'API |
| `TestServer_WebhookSignatureFromProject` | Projet avec `webhook_signatures: [secret1]` → POST webhook avec signature valide → 202 ; POST sans signature → 401 |
| `TestServer_WebhookSignatureFallbackToGlobal` | Projet sans webhook_signatures, global default `[global-secret]` → POST avec `global-secret` → 202 |
| `TestServer_IPRestrictionFromProject` | Projet avec `allowed_ips: [10.0.0.0/8]` → POST depuis `10.1.2.3` → 202 ; POST depuis `192.168.0.1` → 403 |
| `TestServer_IPRestrictionFallbackToGlobal` | Projet sans `allowed_ips`, global `[192.168.0.0/16]` → POST depuis `192.168.1.1` → 202 |
| `TestServer_ReplayTokenFromProject` | Projet avec `replay_token: "tok"` → `POST /replay/<id>` avec `Bearer tok` → 202 ; sans token → 401 |
| `TestServer_ReplayTokenFallback` | Projet sans replay_token, global default `"global-tok"` → `Bearer global-tok` → 202 |
| `TestServer_SecureChannelEncryption` | Projet avec `encryption_enabled: true` + `encryption_public_keys: [key1]` → SSE `/events/<id>?pubkey=key1` → 200, messages chiffrés ; SSE sans pubkey → 404 ; SSE avec mauvaise pubkey → 404 |
| `TestServer_PlaintextChannel` | Projet sans encryption → SSE `/events/<id>` → 200, messages en clair |
| `TestServer_CORSOrigin` | Global `cors_origin: "https://example.com"` → SSE response header `Access-Control-Allow-Origin: https://example.com` |
| `TestServer_MaxBodySizeProject` | Projet `max_body_size: 100` → POST body 50 octets → 202 ; POST body 200 octets → 413 |
| `TestServer_MaxBodySizeGlobal` | Pas de projet max_body_size → global `max_body_size: 100` → POST body 50 octets → 202 ; POST body 200 octets → 413 |
| `TestServer_FooterInUI` | Global `footer: "My Footer"` → `GET /<project_id>` → page HTML contient "My Footer" |
| `TestServer_AdminPageProtected` | `GET /admin` sans cookie → redirect login ; avec cookie admin → 200, HTML contient "Projects" |
| `TestServer_TrustProxy` | Global `trust_proxy: true` → `X-Forwarded-For` est utilisé pour le client IP dans les logs |
| `TestServer_MultiNodeRaft` | Démarre 3 noeuds Raft avec `--raft-peers` → un leader est élu ; l'API répond sur tous les noeuds (forwarding automatique vers le leader) |
| `TestServer_RaftLeaderRedirect` | Requête d'écriture sur un follower → redirigée vers le leader → réponse 200 |
| `TestServer_NoBootstrap_SetupMode` | Démarre sans `--bootstrap-config-file` → l'API renvoie `{"setup_mode": true}` sur `/api/me` |
| `TestServer_Restart_Persistence` | Démarre avec bootstrap → crée un projet → arrête → redémarre → le projet est toujours là |

---

### Tests de non-régression sur l'existant

| Fichier existant | Action |
|-----------------|--------|
| `gosmee/server_test.go` | Les tests `TestEventBroker`, `TestWebhookSignatureValidation`, `TestHandleWebhookPost`, `TestHandleReplayPost`, `TestHandleEventsGet`, `TestHandleEventsGetCORSOrigin`, `TestServeIndexAndNewURL`, `TestIPRestrictions` doivent rester verts. Les tests `TestHandleWebhookPost`, `TestHandleReplayPost`, `TestHandleEventsGet` sont adaptés pour injecter un mock de `RaftStore` au lieu de flags CLI. |
| `gosmee/auth_test.go` | `TestRequireAuthMiddleware`, `TestLoginHandlerPOST_Internal`, `TestLogoutHandler`, `TestFullProtectedFlow` adaptés pour utiliser le `RaftStore` comme source d'utilisateurs au lieu de `auth-config-file`. Les tests OIDC restent inchangés. |
| `gosmee/protected_channels_test.go` | `TestLoadProtectedChannels` déplacé dans `store/` ; l'ancienne version est supprimée. |
| `gosmee/client_test.go` | Ajuster `newTestContext()` si les flags changent. |
| `gosmee/app_test.go` | Inchangé. |
| `gosmee/crypto_test.go` | Inchangé. |
| `gosmee/replay_test.go` | Inchangé. |
| `gosmee/version_test.go` | Inchangé. |

### Couverture cible

| Package | Coverage cible |
|---------|---------------|
| `gosmee/store/` (nouveau) | ≥ 90% |
| `gosmee/` (modifié) | ≥ 85% (inchangé par rapport à l'existant, les tests sont adaptés) |

---

## Changements hors-scope

- **Stockage/replay des messages dans le bus** : pas traité ici (discussion séparée, recommandation NATS JetStream)
- **Migration automatique** des anciens fichiers de conf vers le bootstrap : pas traité (manuel : l'opérateur crée le `bootstrap.yaml`)
- **Chiffrement TLS entre noeuds Raft** : pas dans cette V1 (utiliser un réseau privé / VPN entre noeuds)
- **Multi-tenancy avancé** : un projet = un tenant, pas de hiérarchie organisationnelle
