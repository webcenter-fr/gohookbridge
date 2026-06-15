# Cleanup Deprecated Flags

## Objectif

Supprimer les flags CLI obsolètes depuis que NATS est core, que le TTL est géré par channel, et que la configuration globale/p channel passe par le bootstrap.

## Flags modifiés

### `gohookbridge/flags.go` — `serverFlags`

| Flag | Action | Raison |
|------|--------|--------|
| `nats-port` | **Modifier usage** : retirer "Set to 0 to disable...". Garder le flag. | NATS est core, plus optionnel |
| `nats-buffer-ttl` | **Supprimer** | TTL désormais piloté par `GlobalConfig.Defaults.MessageTTLSeconds` + `Channel.MessageTTLSeconds` |
| `nats-buffer-size` | **Conservé** | Paramètre infrastructure nœud, pas une config business global/channel |

## Propagation dans le code

### 1. `gohookbridge/nats/broker.go`

- Supprimer `BufferTTL` du struct `Config` (TTL est maintenant par channel)
- Garder `BufferSize` dans `Config` (flag infrastructure conservé)
- Supprimer `if cfg.Port == 0 { return nil, nil }` → NATS toujours démarré
- `NewRingBuffer` appelé avec `cfg.BufferSize` et un `maxAge` par défaut (ex: `24 * time.Hour`)

### 2. `gohookbridge/nats/buffer.go`

- Ajouter constante `DefaultMaxAge = 24 * time.Hour`
- Le `maxAge` global sert de fallback ; `SetChannelTTL` override par channel

### 3. `gohookbridge/server.go`

- Retirer `BufferTTL` de `natsCfg` (ligne 866)
- Supprimer tous les `if broker != nil` (devenu dead code) → 6 occurrences :
  - L288 : `if broker != nil && chConfig.MessageTTLSeconds > 0` → `if chConfig.MessageTTLSeconds > 0`
  - L595 : `if broker != nil && pubKey != nil` → garder l'erreur "protected channels not supported"
  - L627 : `if broker != nil {` → toujours le chemin NATS
  - L808 : `if broker != nil {` dans `publishEvent` → toujours NATS
  - L873 : `if broker != nil {` → toujours exécuté
  - L1009 : `if n.broker != nil` → toujours vrai
- Simplifier `publishEvent()` : retirer la branche `else` (EventBroker/SSE)
- Simplifier `handleEventsGet()` : retirer la branche `else` (EventBroker)
- Retirer `sse.New()` et `NewEventBroker()` de `serve()` s'ils ne servent plus

### 4. `gohookbridge/server_test.go`

- Adapter les tests qui passent `nil` comme broker → utiliser `newNatsBroker(t, port)` avec port unique
- Tests concernés : `TestHandleWebhookPostWithoutNATS`, `TestHandleReplayPostWithoutNatS`, `TestHandleEventsGet` (chemin EventBroker)

## Fichiers impactés

| Fichier | Changement |
|---------|-----------|
| `gohookbridge/flags.go` | Supprimer 2 flags, modifier 1 usage |
| `gohookbridge/nats/broker.go` | Nettoyer Config, retirer disable path |
| `gohookbridge/nats/buffer.go` | Ajouter constantes |
| `gohookbridge/server.go` | Simplifier, retirer dead code |
| `gohookbridge/server_test.go` | Adapter les tests |
