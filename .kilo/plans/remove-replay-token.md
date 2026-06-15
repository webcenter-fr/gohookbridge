# Plan: Remove Replay Token and Restrict Replay to UI Auth

## Objective
Remove the "Replay Token" concept entirely and restrict the event replay functionality exclusively to the UI-protected API (`/api/channels/{channel}/events/{eventId}/replay`), ensuring it uses the same authentication mechanism as the rest of the UI.

## 1. Backend Changes (`gohookbridge/`)
- **`store/types.go`**:
  - Remove `ReplayToken string` field from the `Channel` struct.
  - Remove `ReplayToken string` field from the `DefaultChannelConfig` struct.
  - Remove the `ReplayToken` fallback logic inside the `resolveChannelConfig` function.
- **`store/raft.go`**:
  - Delete the `ValidateReplayToken` function entirely.
- **`server.go`**:
  - Remove the `replayPath` constant definition (`/replay/{channel:...}`).
  - Delete the `handleReplayPost` function completely (this was the external `/replay/{channel}` endpoint).
  - Remove the route registration: `restrictedRouter.Post(replayPath, handleReplayPost(broker, rs))`.
  - In `handleEventReplay`: Strip out all `X-Replay-Token` and `Bearer` token extraction and `ValidateReplayToken` checks. The endpoint is already mounted under `/api` and protected by `RequireAuthDynamic`, satisfying the requirement to use the same auth as the UI.
- **Backend Tests**:
  - `server_test.go`: Remove the `makeReplayRequest` helper and any test cases validating replay tokens or the removed `/replay/{channel}` endpoint.
  - `store/raft_test.go`: Delete `TestRaftStore_ValidateReplayToken`.

## 2. Frontend Changes (`web/`)
- **`web/src/api/client.ts`**:
  - Remove `replay_token?: string` from the `Channel` interface.
  - Remove `replay_token: string` from the `defaults` object within the `GlobalConfig` interface.
- **`web/src/views/ChannelDetailView.vue`**:
  - Remove the `<n-form-item label="Replay Token">` block.
  - Remove `replay_token: ''` from the `form` reactive object initialization.
  - Remove `form.replay_token = channel.value.replay_token || ''` from the channel data loading logic.
  - Remove `replay_token: form.replay_token || undefined` from the `api.updateChannel` payload.
- **`web/src/views/AdminGlobalView.vue`**:
  - Remove `replay_token: ''` from the `defaults` object in the `handleSave` function payload.

## 3. Validation & Testing Steps
1. Run `make lint-go` and `make test` to verify backend integrity and test suite passage.
2. Run `cd web && npx vue-tsc --noEmit` to ensure frontend TypeScript compilation succeeds without type errors.
3. Run `make build` to validate the complete Go + Vue.js build pipeline.

## Clarifying Questions
1. The `replay` CLI command in `replay.go` (which fetches GitHub hook deliveries and forwards them to a target URL) is a standalone client-side tool feature and does not interact with the server's `/replay` API. Should this CLI feature remain untouched? (I assume yes, but want to confirm).
2. Are there any external automation scripts or CI/CD pipelines in your environment that might currently be calling the `/replay/{channel}` endpoint that will be removed?
