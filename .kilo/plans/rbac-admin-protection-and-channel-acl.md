# RBAC: Protect Admin Role & Fix Channel ACL

## Problem 1: Admin role can be removed from user bindings

The `updateBinding` handler (`gohookbridge/store/api.go:393`) and `updateUser` handler (`gohookbridge/store/api.go:320`) allow removing the "admin" role from any user, which could lock out all admins.

### Solution

**Backend** (`gohookbridge/store/api.go`):

1. In `updateBinding` (line 393), after decoding the binding, check if the user currently has the "admin" role. If so, verify the new roles list also contains "admin". If not, return `400 Bad Request` with error "cannot remove admin role".

2. In `updateUser` (line 320), apply the same check: if the existing user has "admin" role and the new roles list doesn't include it, return `400 Bad Request`.

3. In `deleteUser` (line 366), check if the user being deleted has the "admin" role. If so, check if there are other admin users. If this is the last admin, return `400 Bad Request` with error "cannot delete the last admin user".

**Frontend** (`web/src/views/AdminRBACView.vue`):

4. In the binding edit modal, filter the `roleOptions` to disable the "admin" option when the user being edited already has the admin role. Use Naive UI's `disabled` property on the option.

**Frontend** (`web/src/views/AdminUsersView.vue`):

5. Same as above: when editing a user who has the admin role, disable the "admin" option in the roles select.

**Tests** (`gohookbridge/store/api_test.go` or new test file):

6. Add test: `TestUpdateBinding_CannotRemoveAdminRole` - verify that updating a binding to remove admin role returns an error.
7. Add test: `TestUpdateUser_CannotRemoveAdminRole` - verify that updating a user to remove admin role returns an error.
8. Add test: `TestDeleteUser_CannotDeleteLastAdmin` - verify that deleting the last admin user returns an error.

---

## Problem 2: Channel ACL "Access Control" tab is empty for admin

The channel ACL endpoints (`/api/channels/{id}/acl`) are under the `PermChannelRead` middleware. For admin users with `*` permission and `Channels: ["*"]`, the permission check should pass. However, the ACL add/delete operations should require `PermChannelWrite` instead of just `PermChannelRead`.

### Root Cause Analysis

Looking at the route registration in `gohookbridge/store/api.go:30-47`:
```go
r.Route("/channels", func(r chi.Router) {
    r.Use(RequirePermission(rs, PermChannelRead))
    ...
    r.Route("/{id}", func(r chi.Router) {
        r.Use(h.channelCtx)
        ...
        r.Get("/acl", h.listChannelACL)
        r.Post("/acl", h.addChannelACLEntry)
        r.Delete("/acl/{entryID}", h.deleteChannelACLEntry)
    })
})
```

The ACL add/delete endpoints inherit `PermChannelRead`, but they should require `PermChannelWrite`. More importantly, ACL management is an RBAC operation and should also be accessible to users with `rbac:write` permission.

### Solution

**Backend** (`gohookbridge/store/api.go`):

1. Move the ACL `POST` and `DELETE` endpoints out of the `PermChannelRead` middleware group. Instead, apply a custom permission check that allows access if the user has EITHER `PermChannelWrite` OR `PermRBACWrite` for the given channel.

2. Create a new middleware function `RequireChannelACLPermission` in `gohookbridge/store/acl.go` that checks:
   - User has `PermChannelWrite` for the channel, OR
   - User has `PermRBACWrite` (global), OR
   - User has admin role (`*` permission)

3. Apply this middleware to the `POST /acl` and `DELETE /acl/{entryID}` routes.

4. Keep `GET /acl` under `PermChannelRead` (already correct).

**Frontend** (`web/src/views/ChannelDetailView.vue`):

5. The ACL tab should only show the "Add Entry" button and "Delete" actions if the user has write access. Add a computed property `canManageACL` that checks if the user has `channel:write` or `rbac:write` permission. Conditionally render the "Add Entry" button and delete actions based on this.

**Tests**:

6. Add test: `TestChannelACL_ListRequiresChannelRead` - verify that listing ACLs requires channel read permission.
7. Add test: `TestChannelACL_AddRequiresChannelWriteOrRBACWrite` - verify that adding ACL entries requires channel write or RBAC write permission.

---

## Implementation Order

1. Backend: Add admin role protection to `updateBinding`, `updateUser`, `deleteUser`
2. Backend: Write tests for admin role protection
3. Frontend: Disable admin role option in binding/user edit forms
4. Backend: Fix channel ACL permission checks
5. Backend: Write tests for channel ACL permissions
6. Frontend: Conditionally show ACL management controls based on permissions
7. Run `make lint` and `make test` to verify
