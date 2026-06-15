# Channel Delete Feature

## Summary

Add UI for deleting channels — both from the channel list (multi-select bulk delete) and from within the channel detail page. The backend `DELETE /api/channels/{id}` already exists; only frontend UI and additional tests are needed.

---

## Current State

| Layer | Component | Status |
|---|---|---|
| Backend | `DELETE /api/channels/{id}` route + handler | Already exists (`api.go:38, 164-171`) |
| Backend | `RaftStore.DeleteChannel()` | Already exists (`raft.go:303`) |
| Backend | Store-level delete test | Already exists (`raft_test.go:71-82`) |
| Frontend | `api.deleteChannel()` | Already exists (`client.ts:127-129`) |
| Frontend | `channelsStore.deleteChannel()` | Already exists (`channels.ts:30-33`) |
| Frontend | UI for deletion | **Missing** |
| Backend | HTTP handler test for DELETE | **Missing** |

---

## Implementation Plan

### 1. Backend — HTTP Handler Test for Delete Channel

**File:** `gohookbridge/store/api_test.go`

Add a test function `TestDeleteChannel` that:
- Creates a channel via API
- Deletes it via `DELETE /api/channels/{id}`
- Asserts 204 status code
- Gets the channel and asserts 404 not found

Add a test for "delete nonexistent channel" returning 500 (or 404).

### 2. Frontend — Bulk Delete in ChannelsView.vue

**File:** `web/src/views/ChannelsView.vue`

Add multi-select delete:
- Add `row-key="id"` to `n-data-table` for selection tracking
- Add `checked-row-keys` v-model for selected channel IDs
- Add a "Delete Selected" `n-button` (type: error, shown only when selection > 0)
- On click, open a confirmation `n-modal` with warning text listing the channels to delete
- On confirm, call `channelsStore.deleteChannel(id)` for each selected channel
- Show success/error messages per channel
- Clear selection after deletion and refresh the list

Imports needed: `useDialog` or custom `n-modal` for confirmation.

### 3. Frontend — Delete Button in ChannelDetailView.vue

**File:** `web/src/views/ChannelDetailView.vue`

Add delete action in the Settings tab:
- Add a "Delete Channel" `n-button` (type: error, secondary) at the bottom of the settings form
- On click, open a confirmation `n-modal` with:
  - Warning text: "Are you sure you want to delete channel '{channelId}'? This action cannot be undone."
  - Text input to type the channel ID as extra confirmation
  - Cancel and Delete buttons
- On confirm, call `api.deleteChannel(channelId)`
- On success: show success message, navigate to `/channels`
- On error: show error message

### 4. Frontend — Test Infrastructure Setup

**Files:** `web/package.json`, `web/vitest.config.ts`, `web/src/__tests__/`

Add vitest + @vue/test-utils + jsdom:
- `npm install -D vitest @vue/test-utils jsdom`
- Create `web/vitest.config.ts` with Vue plugin and jsdom environment
- Add `"test": "vitest run"` script to `web/package.json`

### 5. Frontend — Tests for Delete Feature

**New file:** `web/src/views/__tests__/ChannelsView.spec.ts`
- Test that "Delete Selected" button appears when rows are checked
- Test that confirmation modal opens on click
- Test that `deleteChannel` is called on confirm

**New file:** `web/src/views/__tests__/ChannelDetailView.spec.ts`
- Test that "Delete Channel" button renders in settings tab
- Test that confirmation modal appears
- Test that `deleteChannel` is called and navigation happens on success

### 6. Backend — Integration Test for Delete Handler

**File:** `gohookbridge/store/api_test.go`

Add tests:
- Create channel → delete → verify 204 → get returns 404
- Delete nonexistent channel → verify error response

---

## Files Changed

1. `web/package.json` — add vitest deps and test script
2. `web/vitest.config.ts` — new file, vitest config
3. `web/src/views/ChannelsView.vue` — add multi-select + bulk delete UI
4. `web/src/views/ChannelDetailView.vue` — add delete button in settings tab
5. `web/src/views/__tests__/ChannelsView.spec.ts` — new file, unit tests
6. `web/src/views/__tests__/ChannelDetailView.spec.ts` — new file, unit tests
7. `gohookbridge/store/api_test.go` — add HTTP handler delete tests
