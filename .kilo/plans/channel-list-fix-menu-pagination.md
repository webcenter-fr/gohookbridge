# Plan: Channel List Fix, Menu, and Pagination

## Summary

Three related changes: (1) Fix channel list not updating on UI after creation, (2) Add a Channels menu to the sidebar for navigation, (3) Add pagination/infinite scroll with search filter to the channel list.

---

## Root Cause Analysis: Channel List Always Empty

### Backend Bug in `gohookbridge/store/acl.go:83-111` (`UserChannels`)

The `listChannels` API handler (`api.go:70-95`) filters all channels through `UserChannels()`, which only ever returns `user.Channels` — the explicit list of channel IDs assigned to the user. But `UserChannels()` only returns ALL channel IDs (via `ListChannels()`) when BOTH conditions are met:

1. User has a role with `*` permission
2. User has `*` in `user.Channels`

The dev-admin created via `--dev-admin` has `Roles: ["admin"]` but `Channels: nil`, so condition 2 fails and `UserChannels()` returns empty. The permission middleware (`RequirePermission`) correctly grants access (admin has `*`), but then `listChannels` filters everything out. Result: **empty channel list forever**.

The same issue affects bootstrap-created users who have `Roles: ["admin"]` without `Channels: ["*"]`.

### Fix Strategy

Modify `UserChannels()` in `acl.go` to detect when a user has a role with `*` (all) permission and return all channel IDs regardless of whether `*` is in `user.Channels`. The `*` permission implies access to everything — the channel list restriction should not apply to superadmins.

---

## Backend Changes

### 1. Fix `UserChannels()` — `gohookbridge/store/acl.go:83-111`

**Change**: In the loop that checks `p == "*"`, remove the `hasChannelAccess(user.Channels, "*")` precondition. When a user has any role with `*` permission, they should get all channels.

```go
// Before (line 99):
if p == "*" && hasChannelAccess(user.Channels, "*") {

// After:
if p == "*" {
```

This simple 1-line removal allows admin users (role with `*` permission) to see all channels without requiring `*` in their `user.Channels` list.

### 2. Fix `CreateDevAdmin` — `gohookbridge/store/raft.go:667-682`

**Optional safety fix**: Set `Channels: []string{"*"}` on the dev-admin user for explicit compatibility:

```go
user := &User{
    ID:           "admin",
    Username:     "admin",
    PasswordHash: string(hash),
    Roles:        []string{"admin"},
    Channels:     []string{"*"},  // add this
}
```

This is belt-and-suspenders. Even with fix #1, it makes the admin's channel access explicit and consistent with the existing `hasChannelAccess(user.Channels, "*")` check elsewhere.

---

## Frontend Changes

### 3. Channels Sidebar Menu — `web/src/components/AppLayout.vue`

Add a dynamic "Channels" section to the sidebar menu:

- Load channels from `useChannelsStore` on mount
- Add menu items under the Dashboard item for each channel the user can access
- Each menu item routes to `/{channelId}`
- Highlight active channel based on current route

**Changes in `AppLayout.vue`**:
- Import `useChannelsStore`
- Call `channelsStore.fetchChannels()` on mount
- In `menuOptions`, add a section for channels after the dashboard item
- Use `computed` to derive channel menu items from `channelsStore.channels`

### 4. Pagination / Infinite Scroll + Search Filter — `web/src/views/DashboardView.vue`

Replace the simple `NDataTable` listing with a paginated/infinite-scroll table that has search:

**Option A (recommended): Paginated table with search bar**
- Add a search input above the table that filters by channel ID/name client-side
- Add pagination via `n-pagination` component from naive-ui
- Keep the `NDataTable` but slice the data based on current page/pageSize

**Implementation**:
- Add `searchQuery` ref (binds to `n-input` for search)
- Add `currentPage` and `pageSize` (default 10 or 20) refs
- Add `filteredChannels` computed that filters channels by `searchQuery`
- Add `paginatedChannels` computed that slices `filteredChannels`
- Bind `NDataTable` to `paginatedChannels`
- Add `NPagination` below the table
- Reset page to 1 when search query changes

**Files changed**:
- `web/src/views/DashboardView.vue` — add search, pagination
- No new dependencies needed (naive-ui already has `NInput`, `NPagination`)

---

## Files Modified

| File | Change |
|------|--------|
| `gohookbridge/store/acl.go` | Fix `UserChannels()` — remove `hasChannelAccess(channels, "*")` guard for `*` permission |
| `gohookbridge/store/raft.go` | Set `Channels: ["*"]` on dev-admin user |
| `web/src/components/AppLayout.vue` | Add dynamic Channels menu section with `useChannelsStore` |
| `web/src/views/DashboardView.vue` | Add search filter and pagination to channel list |

---

## Verification

1. **Backend fix**: Create a new channel via the UI — it should appear immediately in the list. Reload the page — channels should persist.
2. **Menu**: Sidebar should show "Channels" section listing all accessible channels. Clicking navigates to the channel detail page.
3. **Pagination/Search**: More than 10 channels should paginate. Typing in the search box filters the list. Clear search restores full list.
4. Run existing Go tests: `cd /projects/gohookbridge && go test ./gohookbridge/...`
5. Verify frontend builds: `cd /projects/gohookbridge/web && npm run build`
