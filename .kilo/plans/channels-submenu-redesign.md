# Channels Sub-Menu Redesign Plan

## Overview
Restructure the web UI to add a dedicated Channels page with infinite scrolling, simplify the dashboard to stats + shortcuts, remove channel names from the sidebar, fix SSE connection issues, and add a Clients tab with CLI command generation on the channel detail page.

---

## 1. Fix SSE Connection (Bug Fix)

**Root cause:** In `ChannelView.vue:42`, `eventsStore.connect(channel)` passes a `ref<Channel | null>` instead of a channel ID string. The `connect()` function uses it directly in the URL: `/events/${channel}`, which produces `/events/[object Object]`.

**Fix in `web/src/stores/events.ts`:**
- No changes needed here — it correctly expects `channel: string`

**Fix in `web/src/views/ChannelView.vue`:**
- Line 42: Change `eventsStore.connect(channel)` → `eventsStore.connect(channelId)` (the `channelId` const already exists on line 63)
- Also fix EventFeed `:channel` prop to pass `channelId` instead of `channel` (since `EventFeed` expects `channel: string`)
- On mount, auto-connect: call `eventsStore.connect(channelId)` after loading channel data

---

## 2. Restructure Routing

**New route structure:**
```
/                    → Dashboard (stats + shortcuts)
/channels             → ChannelsView (full table with infinite scroll)
/channels/:id         → ChannelDetailView (Data/Settings/Clients tabs)
/admin/global         → (unchanged)
/admin/users          → (unchanged)
/admin/rbac           → (unchanged)
/admin/oidc           → (unchanged)
```

**Changes to `web/src/router/index.ts`:**
- Replace `/:channel` route with `/channels/:id` named `channel-detail`
- Add `/channels` route named `channels`
- Update `handleMenuSelect` in AppLayout for new route names

---

## 3. Create ChannelsView (`/channels`)

**New file: `web/src/views/ChannelsView.vue`**

- Copy the channels table from DashboardView (columns, search, create modal)
- Replace client-side pagination with Naive UI's `n-data-table` `remote` mode using the existing `useChannelsStore`
- Add infinite scroll: use `n-data-table` with `virtual-scroll` prop for large channel lists, OR implement `on-load` / scroll-based loading via `n-scrollbar` + load-more pattern
- Keep the "New Channel" button and create modal
- Expose a `n-data-table` with columns: ID, Name, Signatures, Encryption status, Actions (View)
- Search box filters locally

---

## 4. Create ChannelDetailView (`/channels/:id`)

**New file: `web/src/views/ChannelDetailView.vue`**

Three tabs, defaulting to "data":

### Tab 1: Data (default, auto-connect)
- On mount, immediately call `eventsStore.connect(channelId)` 
- Show the EventFeed component with connection status
- Controls: Connect/Disconnect/Clear buttons
- Auto-disconnect on component unmount

### Tab 2: Settings
- Move existing settings form from ChannelView
- Keep all fields: ID (disabled), Name, Description, Webhook Signatures, Allowed IPs, Max Body Size, Replay Token
- Add encryption-related fields: Encryption Enabled (switch), Encryption Public Keys (dynamic input)
- Save/Replay buttons

### Tab 3: Clients
- **CLI Command Generator:**
  - Detect if channel has encryption enabled
  - If encryption disabled: show `gohookbridge client <server-url>/<channel-id> <target-url>`
  - If encryption enabled: show keygen + client commands:
    ```
    gohookbridge keygen --key-file ./gohookbridge-key.json
    gohookbridge client --encryption-key-file ./gohookbridge-key.json <server-url>/<channel-id> <target-url>
    ```
  - Server URL derived from `window.location.origin`
  - Use `n-code` component with a copy button for each command
- **Registered Clients:** List `encryption_public_keys` from the channel config (the authorized public keys that can connect)

**Type additions to `web/src/api/client.ts`:**
- `Channel` interface already has `encryption_enabled?` and `encryption_public_keys?` fields
- No changes needed

**Store additions to `web/src/stores/channels.ts`:**
- Already has all needed methods (`fetchChannels`, `updateChannel`, etc.)

---

## 5. Update DashboardView (`/`)

**Modify: `web/src/views/DashboardView.vue`**

Replace the full channels table with:
1. **Stats card:** "You can access X channels" (using `channelsStore.channels.length` or the filtered list from user info)
2. **Channel shortcuts:** Grid of n-card tiles, one per channel, linking to `/channels/<id>`. Show channel name/ID, encryption badge if enabled.
3. Keep "New Channel" button at top

---

## 6. Update AppLayout Sidebar

**Modify: `web/src/components/AppLayout.vue`**

- **Remove**: Individual channel entries from the sidebar menu (`menuOptions` computed)
- **Add**: "Channels" menu item with key `'channels'`, placed between Dashboard and admin items
- **Update**: `handleMenuSelect` to route `'channels'` → `router.push({ name: 'channels' })`

**New sidebar structure:**
```
📋 Dashboard
📡 Channels      ← NEW
--- divider ---
⚙️ Global Config
👥 Users
🔐 RBAC
🔗 OIDC
```

---

## 7. Files to Create / Modify

| Action | File | Description |
|--------|------|-------------|
| **NEW** | `web/src/views/ChannelsView.vue` | Channels table with infinite scroll + create modal |
| **NEW** | `web/src/views/ChannelDetailView.vue` | Channel page with Data/Settings/Clients tabs |
| **MODIFY** | `web/src/views/DashboardView.vue` | Stats cards + channel shortcut tiles |
| **MODIFY** | `web/src/views/ChannelView.vue` | Replaced by ChannelDetailView (can be deleted or thinned) |
| **MODIFY** | `web/src/components/AppLayout.vue` | Remove channel names, add "Channels" menu item |
| **MODIFY** | `web/src/router/index.ts` | Add /channels and /channels/:id routes |
| **MODIFY** | `web/src/stores/events.ts` | No changes (already correct) |
| **MODIFY** | `web/src/api/client.ts` | No changes needed (Channel type already has encryption fields) |

---

## 8. Implementation Order

1. Fix SSE connection bug in `ChannelView.vue` (or new ChannelDetailView) — unblocks data viewing
2. Add `/channels` and `/channels/:id` routes
3. Create `ChannelsView.vue` with table + infinite scroll
4. Create `ChannelDetailView.vue` with 3 tabs (Data auto-connect, Settings, Clients)
5. Update `AppLayout.vue` sidebar (remove channel names, add Channels link)
6. Update `DashboardView.vue` with stats cards + shortcuts
7. Clean up old `ChannelView.vue` references

## 9. Infinite Scrolling Strategy

Use Naive UI's `n-data-table` with `virtual-scroll` prop. Since the channels list is typically small (< 1000), virtual scrolling handles performance while keeping things simple. The store already loads all channels at once via `fetchChannels()`. No API changes needed for pagination.

If the list grows large in the future, the backend already supports the full list (filtered by ACL), so virtual scrolling over the full list is appropriate.

## 10. CLI Command Generation

The `window.location.origin` provides the base URL. Commands are constructed as:
- **No encryption:** `gohookbridge client ${origin}/${channelId} http://localhost:8080`
- **With encryption:** Two-step:
  1. `gohookbridge keygen --key-file ./gohookbridge-key.json`
  2. `gohookbridge client --encryption-key-file ./gohookbridge-key.json ${origin}/${channelId} http://localhost:8080`
- Copy button uses `navigator.clipboard.writeText()`
