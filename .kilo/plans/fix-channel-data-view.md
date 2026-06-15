# Fix Channel Data View - SSE Connection & Test Payload Sender

## Problem

1. **SSE connection fails** for channels with names shorter than 12 characters (e.g., "test"). The Go route pattern `[a-zA-Z0-9_-]{12,64}` is stricter than the store validation (`min=1`), causing short channel names to fall through to the SPA handler which returns the HTML index page instead of SSE.

2. **No way to send test payloads** from the UI for debugging webhooks. Users need an easy way to send payloads (GitHub-style and raw JSON) to test channel delivery.

## Root Cause

In `gohookbridge/server.go:37`:
```go
channelIDPattern = "[a-zA-Z0-9_-]{12,64}"
```
The route pattern requires 12-64 chars, but `gohookbridge/store/types.go:11` validates channel IDs as `min=1,max=64`. A channel named "test" (4 chars) passes store validation but fails to match any HTTP route.

## Plan

### 1. Fix SSE Connection (Backend)

**File: `gohookbridge/server.go`**
- Change `channelIDPattern` from `"{12,64}"` to `"{1,64}"` to align with the store's `min=1` validation.

### 2. Add Test Payload Sender (Backend + Frontend)

#### Backend: New API endpoint

**File: `gohookbridge/server.go`**
- Add a new route `POST /api/send/{channel}` on the `apiRouter` (behind auth, no IP restriction)
- Handler reads JSON body, publishes to the event broker/NATS, returns 202
- Reuse existing publish logic from `handleWebhookPost`

**File: `web/src/api/client.ts`**
- Add `sendTestPayload(channelId, payload)` method to `ApiClient`

#### Frontend: Test Payload UI

**File: `web/src/views/ChannelDetailView.vue`**
- Add a new "Send Test" tab (or section within the Data tab) with:
  - **GitHub payload builder**: repo name input + event type dropdown (push, pull_request, issues, release, ping, etc.) + "Generate & Send" button
  - **Raw payload editor**: textarea for JSON + "Send" button
- Show success/error feedback via `useMessage()`
- Only enable when SSE is connected (indicates the channel is active)

### 3. Backend Route Registration

In `gohookbridge/server.go`, add to apiRouter:
```go
apiRouter.Post("/send/{channel}", handleTestPayloadSend(eventBroker, broker, rs))
```

The handler:
- Gets channel from URL param
- Reads JSON body
- Optionally validates channel exists
- Publishes to broker/eventBroker with timestamp + body encoding
- Returns 202

### Files Changed

| File | Change |
|------|--------|
| `gohookbridge/server.go` | Fix channelIDPattern to 1-64 chars; add `handleTestPayloadSend` handler and route |
| `web/src/api/client.ts` | Add `sendTestPayload()` method |
| `web/src/views/ChannelDetailView.vue` | Add test payload sender UI with GitHub builder + raw JSON editor |
