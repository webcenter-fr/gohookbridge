# Plan: Channel UI Rework & Feature Enhancements

## Overview
Revoir l'UI des channels et ajouter les features manquantes : TTL, génération de secret webhook, chiffrement configurable, replay par event, display bodyB only, unités max body size.

---

## 1. Data Tab — Events Display

### Backend
- **`POST /api/channels/{id}/events/{eventId}/replay`** — Nouvel endpoint pour rejouer un event spécifique.
  - Re-publish l'event stocké (bodyB décodé) dans le channel via NATS/EventBroker.
  - Nécessite permission `channel:write`.
  - L'event ID est l'index/timestamp côté serveur, ou on stocke les events dans un buffer adressable.

- **Modifier `handleWebhookPost`** — Ajouter un ID unique (UUID) à chaque event publié, stocké dans le payload (`"event_id": "<uuid>"`).

### Frontend — `ChannelDetailView.vue`
- **Supprimer le tab "Send Test"** (lignes 87-122) entièrement.
- **Le bouton "Send"** dans le Data tab (ligne 15) ouvre le drawer existant (déjà fonctionnel). Renommer "Send Test Payload" → "Send Payload".
- **EventFeed.vue** : Chaque event affiche uniquement le contenu de `bodyB` décodé (base64 → JSON).
  - Par défaut : JSON compressé (1 ligne, `JSON.stringify(parsed)`).
  - Icône flèche ▼ (chevron-down) au début de chaque event → toggle vers pretty JSON (`JSON.stringify(parsed, null, 2)`).
  - Bouton "Replay" à côté de chaque event → appelle `POST /api/channels/{id}/events/{eventId}/replay`.

### Frontend — `JsonViewer.vue`
- Refactor pour accepter un mode `compact | pretty`.
- Prop `expanded: boolean`, toggle via icône.

---

## 2. Settings Tab — Channel Configuration

### Backend — `store/types.go` — Channel struct
```go
type Channel struct {
    ID                string `json:"id"`
    Name              string `json:"name"`
    Description       string `json:"description,omitempty"`
    WebhookSecret     string `json:"webhook_secret,omitempty"`      // WAS: WebhookSignatures []string
    AllowedIPs        []string `json:"allowed_ips,omitempty"`
    MaxBodySize       int    `json:"max_body_size,omitempty"`
    MessageTTLSeconds int    `json:"message_ttl_seconds,omitempty"` // NEW: 0 = use global default
    ReplayToken       string `json:"replay_token,omitempty"`
    EncryptionMode    string `json:"encryption_mode,omitempty"`     // NEW: "", "server_side", "provider_side"
    EncryptionKey     string `json:"encryption_key,omitempty"`      // NEW: symmetric key (AES) or decryption key (NaCl private)
    EncryptionPubKeys []string `json:"encryption_public_keys,omitempty"` // used in provider_side mode
}
```

### Migration
- **WebhookSignatures → WebhookSecret** : Migrate en gardant le premier élément du tableau s'il existe. Supporter l'ancien champ en lecture, écrire le nouveau.
- **MessageTTL** : Ajouter dans `DefaultChannelConfig` aussi (`message_ttl_seconds`).
- **Encryption fields** : Nouveaux champs, pas de migration nécessaire.

### Backend Changes
1. **`store/fsm.go` + `store/raft.go`** — Mettre à jour les fonctions de CRUD channel pour supporter les nouveaux champs.
2. **`server.go`** — `validateWebhookSignature` utilise `WebhookSecret` (single) au lieu de `WebhookSignatures` (array).
3. **`server.go`** — `handleWebhookPost` : Après validation signature, si `EncryptionMode == "server_side"`, chiffrer le body avec AES-256-GCM avant de publier dans le channel.
4. **`server.go`** — `handleEventsGet` : Si `EncryptionMode == "server_side"`, le serveur peut déchiffrer pour l'affichage UI (l'UI admin est trusted). Le client SSE reçoit les données chiffrées sauf si l'UI admin les demande en clair.
5. **`nats/buffer.go`** — RingBuffer cleanup respecte `MessageTTLSeconds` par-channel (en plus du TTL global).
6. **`POST /api/channels/{id}/generate-secret`** — Génère un secret aléatoire (64 chars hex) pour le webhook.
7. **`POST /api/channels/{id}/generate-encryption-key`** — Génère une clé AES-256 (32 bytes, base64) pour `server_side` OU une paire NaCl pour `provider_side`.
8. **Nouveau fichier `gohookbridge/encryption.go`** — AES-256-GCM encrypt/decrypt (mode `server_side`).

### Global Config (`AdminGlobalView.vue`)
- Ajouter `message_ttl_seconds` dans les defaults globaux.
- Ajouter `max_body_size` avec unit selector.

### Frontend — `ChannelDetailView.vue` Settings Tab
- **Webhook Secret** : Remplacer `n-dynamic-input` par un `n-input` simple + bouton "Generate" (appelle `/api/channels/{id}/generate-secret`).
- **Message TTL** : Nouveau champ `n-input-number` avec unité (secondes/minutes/heures).
- **Encryption** :
  - `n-select` : `None`, `Server-side`, `Provider-side`.
  - Si `Server-side` : bouton "Generate Key" + `n-input` pour la clé AES (masquée).
  - Si `Provider-side` : `n-dynamic-input` pour les public keys (NaCl box) + section "Client Key Generation" avec commandes.
- **Max Body Size** : `n-input-number` + `n-select` (B, KiB, MiB, GiB). Convertir en bytes avant save.
- **Supprimer le bouton "Replay Events"** (ligne 52).
- **Supprimer le champ "Encryption Enabled" switch** — remplacé par le select Encryption Mode.

### Frontend — `api/client.ts` — Channel interface
- Mettre à jour l'interface `Channel` avec les nouveaux champs.
- Ajouter méthodes `generateWebhookSecret(channelId)`, `generateEncryptionKey(channelId, mode)`, `replayEvent(channelId, eventId)`.

### Frontend — `ChannelsView.vue`
- Colonne "Signatures" → "Webhook Secret" : afficher "Set" / "Not set" au lieu de la valeur.
- Colonne "Encryption" : afficher le mode.

---

## 3. Clients Tab

### Frontend — `ChannelDetailView.vue` Clients Tab
- **"Authorized Public Keys"** : Renommer en "Authorized Client Keys" et clarifier que ce sont les clés publiques NaCl des clients autorisés à souscrire (mode provider_side uniquement).
- Afficher conditionnellement selon `encryption_mode`.
- Si `server_side` : montrer la commande client standard (pas de clé nécessaire).
- Si `provider_side` : montrer les commandes keygen + client avec `--encryption-key-file`.
- Si `none` : commande client standard.

---

## 4. Implementation Order

### Phase 1 — Backend Core (3-4h)
1. Mettre à jour `store/types.go` (Channel struct, GlobalConfig, migrations)
2. Mettre à jour `store/fsm.go` (bootstrap, CRUD)
3. Mettre à jour `store/raft.go` (ResolveChannel*, ValidateReplayToken)
4. Mettre à jour `store/bolt.go` si nécessaire
5. Mettre à jour `store/bridge.go` (ProtectedChannels adapté)
6. Ajouter `gohookbridge/encryption.go` (AES-256-GCM)
7. Mettre à jour `server.go` (webhook signature single, server-side encryption, event ID, event replay endpoint, generate endpoints)
8. Mettre à jour `nats/buffer.go` (per-channel TTL)

### Phase 2 — Backend Tests (2-3h)
1. Tests unitaires pour `encryption.go`
2. Tests pour les nouveaux champs Channel dans `store/*_test.go`
3. Tests pour les nouveaux handlers HTTP dans `server_test.go`
4. Tests pour le ring buffer per-channel TTL dans `nats/broker_test.go`

### Phase 3 — Frontend (3-4h)
1. Mettre à jour `api/client.ts` (types + méthodes)
2. Mettre à jour `stores/channels.ts` si nécessaire
3. Refactor `JsonViewer.vue` (compact/pretty toggle)
4. Refactor `EventFeed.vue` (bodyB only, replay button, expand toggle)
5. Refactor `ChannelDetailView.vue` (tous les changements settings + data tab)
6. Mettre à jour `ChannelsView.vue` (colonnes)
7. Mettre à jour `AdminGlobalView.vue` (TTL default)
8. Mettre à jour `stores/events.ts` (event ID)

### Phase 4 — Frontend Tests + Integration (2-3h)
1. Tests unitaires composants Vue (Vitest)
2. Tests d'intégration backend (API + publisher + consumer)
3. Tests d'intégration UI/backend (Cypress/Playwright si existant)

---

## 5. Key Design Decisions

| Decision | Rationale |
|---|---|
| WebhookSecret single string | GitHub webhooks use one secret. Multi-secret était overengineered. |
| Encryption mode selector | Plus simple que "Enabled" switch + mode séparé. Clair et explicite. |
| AES-256-GCM pour server-side | Standard, performant, simple à implémenter. NaCl box pour provider-side (asymétrique). |
| Event ID (UUID) dans payload | Permet le replay individuel sans stocker tous les events côté serveur. |
| Per-channel TTL dans ring buffer | MessageTTLSeconds = 0 → utilise le TTL global (`--nats-buffer-ttl`). |
| MaxBodySize units in UI only | Backend store toujours en bytes. Conversion UI → bytes avant save. |

---

## 6. Files Modified (Complete List)

### Backend
- `gohookbridge/store/types.go` — Channel struct, GlobalConfig, resolveChannelConfig
- `gohookbridge/store/fsm.go` — applyBootstrap, applyCreateChannel
- `gohookbridge/store/raft.go` — ResolveChannel*, ValidateReplayToken, CRUD channels
- `gohookbridge/store/bridge.go` — ProtectedChannels adapté
- `gohookbridge/store/api.go` — Nouveaux endpoints generate-secret, generate-key
- `gohookbridge/server.go` — Signature single, server-side encryption, event ID, replay endpoint
- `gohookbridge/crypto.go` — Pas de changement (NaCl box conservé pour provider_side)
- **`gohookbridge/encryption.go`** — NOUVEAU: AES-256-GCM encrypt/decrypt
- `gohookbridge/nats/buffer.go` — Per-channel TTL support
- `gohookbridge/nats/broker.go` — RingBuffer config par channel
- `gohookbridge/flags.go` — Ajout flag `--default-message-ttl`

### Frontend
- `web/src/api/client.ts` — Channel interface, nouvelles méthodes
- `web/src/stores/events.ts` — SSHEvent type (event_id)
- `web/src/stores/channels.ts` — Pas de changement majeur
- `web/src/components/JsonViewer.vue` — Refactor compact/pretty
- `web/src/components/EventFeed.vue` — bodyB only, replay, expand toggle
- `web/src/views/ChannelDetailView.vue` — Refactor majeur (tous les tabs)
- `web/src/views/ChannelsView.vue` — Colonnes mises à jour
- `web/src/views/AdminGlobalView.vue` — Ajout Message TTL default

### Tests
- `gohookbridge/encryption_test.go` — NOUVEAU
- `gohookbridge/server_test.go` — Nouveaux handlers
- `gohookbridge/store/raft_test.go` — Nouveaux champs
- `gohookbridge/store/fsm_test.go` — Bootstrap avec nouveaux champs
- `gohookbridge/nats/broker_test.go` — Per-channel TTL
- `web/src/components/__tests__/` — Tests composants
