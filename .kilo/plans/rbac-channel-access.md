# RBAC & Channel Access Control Implementation Plan

## Overview

Implement a comprehensive RBAC system with:
- **Global RBAC**: Roles and role mappings for internal users, OIDC users, and OIDC groups
- **Channel-level RBAC**: Fixed permission levels (read/write/owner) with scoped mappings
- **OIDC Integration**: Extract groups from configurable claims for group-based access control

## Design Decisions

Based on user requirements:
1. **OIDC Groups**: Configurable claim name per provider (default: "groups"), support both user and group mapping
2. **Channel Owner**: Creator gets owner role by default, multiple owners allowed, owner mappings on groups
3. **Local RBAC Storage**: Separate scoped mappings (not on channel object)
4. **Permission Levels**: Fixed levels (read/write/owner)
5. **Default Mappings**: Auto-create owner mapping for channel creator

## Data Model Changes

### New Types

#### RoleMapping (Global RBAC)
```go
type RoleMapping struct {
    ID           string `json:"id"`
    Type         string `json:"type"`          // "user" or "group"
    Subject      string `json:"subject"`       // user_id or group_name
    Role         string `json:"role"`          // role name
    ChannelScope string `json:"channel_scope"` // "*" or specific channel ID
}
```

#### ChannelRoleMapping (Channel-level RBAC)
```go
type ChannelRoleMapping struct {
    ID        string `json:"id"`
    ChannelID string `json:"channel_id"`
    Type      string `json:"type"`    // "user" or "group"
    Subject   string `json:"subject"` // user_id or group_name
    Role      string `json:"role"`    // "owner", "write", or "read"
}
```

### Updated Types

#### OIDCProvider
```go
type OIDCProvider struct {
    ID           string   `json:"id"`
    Name         string   `json:"name"`
    ClientID     string   `json:"client_id"`
    ClientSecret string   `json:"client_secret"`
    IssuerURL    string   `json:"issuer_url"`
    Scopes       []string `json:"scopes"`
    GroupsClaim  string   `json:"groups_claim"` // NEW: default "groups"
}
```

#### User
```go
type User struct {
    ID           string   `json:"id"`
    Username     string   `json:"username"`
    PasswordHash string   `json:"password_hash,omitempty"`
    OIDCSubjects []string `json:"oidc_subjects,omitempty"`
    Roles        []string `json:"roles"`        // Global role names (deprecated, use RoleMapping)
    Channels     []string `json:"channels"`     // Deprecated, use ChannelRoleMapping
}
```

## Permission Resolution Logic

### Global Permission Check
When checking if user has global permission:
1. Check direct user role mappings (type="user", subject=user_id)
2. Check group role mappings (type="group", subject in user's OIDC groups)
3. For channel-scoped permissions, check if channel_scope matches "*" or the specific channel

### Channel Permission Check
When checking if user has channel permission (read/write/owner):
1. Check channel role mappings for this channel
2. Check if user is channel creator (implicit owner)
3. Check global role mappings with channel_scope matching this channel or "*"

### Permission Hierarchy
- `owner` → implies `write` → implies `read`
- `admin` global role → full access to all channels

## API Changes

### Global RBAC APIs

#### List Global Roles
```
GET /api/rbac/roles
Response: [{name, permissions}]
```

#### Create Custom Global Role
```
POST /api/rbac/roles
Body: {name, permissions}
```

#### List Global Role Mappings
```
GET /api/rbac/mappings
Response: [{id, type, subject, role, channel_scope}]
```

#### Create Global Role Mapping
```
POST /api/rbac/mappings
Body: {type, subject, role, channel_scope}
```

#### Delete Global Role Mapping
```
DELETE /api/rbac/mappings/{id}
```

### Channel RBAC APIs

#### List Channel ACL
```
GET /api/channels/{id}/acl
Response: [{id, channel_id, type, subject, role}]
```

#### Add Channel ACL Entry
```
POST /api/channels/{id}/acl
Body: {type, subject, role}
```

#### Delete Channel ACL Entry
```
DELETE /api/channels/{id}/acl/{entry_id}
```

### OIDC Provider APIs

#### Update OIDC Provider
```
PUT /api/auth/oidc/{id}
Body: {name, client_id, client_secret, issuer_url, scopes, groups_claim}
```

## Implementation Phases

### Phase 1: Data Model & Store Layer
**Files to modify:**
- `gohookbridge/store/types.go` - Add RoleMapping, ChannelRoleMapping types
- `gohookbridge/store/raft.go` - Add store methods for role mappings
- `gohookbridge/store/fsm.go` - Add FSM operations for role mappings
- `gohookbridge/store/bootstrap.go` - Support role mappings in bootstrap

**New methods:**
```go
// Global role mappings
func (rs *RaftStore) CreateRoleMapping(mapping *RoleMapping) error
func (rs *RaftStore) ListRoleMappings() ([]RoleMapping, error)
func (rs *RaftStore) DeleteRoleMapping(id string) error
func (rs *RaftStore) GetUserRoleMappings(userID string) ([]RoleMapping, error)
func (rs *RaftStore) GetGroupRoleMappings(groupName string) ([]RoleMapping, error)

// Channel role mappings
func (rs *RaftStore) CreateChannelRoleMapping(mapping *ChannelRoleMapping) error
func (rs *RaftStore) ListChannelRoleMappings(channelID string) ([]ChannelRoleMapping, error)
func (rs *RaftStore) DeleteChannelRoleMapping(id string) error
func (rs *RaftStore) GetUserChannelRoleMappings(userID string) ([]ChannelRoleMapping, error)
func (rs *RaftStore) GetGroupChannelRoleMappings(groupName string) ([]ChannelRoleMapping, error)
```

### Phase 2: Permission Resolution
**Files to modify:**
- `gohookbridge/store/acl.go` - Update permission resolution logic

**Key changes:**
```go
func UserHasPermission(rs *RaftStore, username string, perm Permission, channelID string) bool {
    // 1. Get user and their OIDC groups
    // 2. Check global role mappings (user + groups)
    // 3. Check channel role mappings (user + groups)
    // 4. Check if user is channel creator
}

func UserChannels(rs *RaftStore, username string) ([]string, error) {
    // Return channels where user has any role mapping
}

func HasChannelRole(rs *RaftStore, username string, channelID string, role string) bool {
    // Check if user has specific role on channel
}
```

### Phase 3: OIDC Integration
**Files to modify:**
- `gohookbridge/server/auth_oidc.go` - Extract groups from OIDC token
- `gohookbridge/store/types.go` - Add GroupsClaim to OIDCProvider

**Key changes:**
```go
func (h *OIDCHandler) CallbackHandler() http.HandlerFunc {
    // Extract groups from configurable claim
    // Store groups in session or context
    // Create/update user with OIDC subjects
}

func extractGroupsFromToken(token map[string]any, claimName string) []string {
    // Extract groups from OIDC token using configurable claim name
}
```

### Phase 4: API Layer
**Files to modify:**
- `gohookbridge/server/api.go` - Add new API endpoints

**New endpoints:**
```go
// Global RBAC
r.Route("/rbac", func(r chi.Router) {
    r.Get("/roles", h.listRoles)
    r.Post("/roles", h.createRole)
    r.Get("/mappings", h.listRoleMappings)
    r.Post("/mappings", h.createRoleMapping)
    r.Delete("/mappings/{id}", h.deleteRoleMapping)
})

// Channel ACL
r.Route("/channels/{id}/acl", func(r chi.Router) {
    r.Get("/", h.listChannelACL)
    r.Post("/", h.addChannelACLEntry)
    r.Delete("/{entryID}", h.deleteChannelACLEntry)
})
```

### Phase 5: Channel Creation Hook
**Files to modify:**
- `gohookbridge/server/api.go` - Auto-create owner mapping on channel creation

**Key changes:**
```go
func (h *apiHandler) createChannel(w http.ResponseWriter, r *http.Request) {
    // Create channel
    // Auto-create owner mapping for creator
    username := GetUsernameFromContext(r.Context())
    h.rs.CreateChannelRoleMapping(&ChannelRoleMapping{
        ChannelID: ch.ID,
        Type:      "user",
        Subject:   username,
        Role:      "owner",
    })
}
```

### Phase 6: Frontend - Global RBAC
**Files to modify:**
- `web/src/api/client.ts` - Add API methods for role mappings
- `web/src/views/AdminRBACView.vue` - Update to manage role mappings

**New UI:**
- List all global role mappings
- Create new role mapping (user/group, role, channel scope)
- Delete role mapping

### Phase 7: Frontend - Channel ACL
**Files to modify:**
- `web/src/api/client.ts` - Add API methods for channel ACL
- `web/src/views/ChannelDetailView.vue` - Add ACL management tab

**New UI:**
- New "Access Control" tab in channel detail view
- List channel ACL entries
- Add ACL entry (user/group, role)
- Delete ACL entry

### Phase 8: Frontend - OIDC Provider Config
**Files to modify:**
- `web/src/api/client.ts` - Update OIDCProvider interface
- `web/src/views/AdminOIDCView.vue` - Add groups claim field

**New UI:**
- Add "Groups Claim" field to OIDC provider form

### Phase 9: Migration
**Files to modify:**
- `gohookbridge/store/migrate.go` - New migration file

**Migration logic:**
```go
func MigrateToRBAC(rs *RaftStore) error {
    // For each user with channels:
    //   - Create channel role mappings (default: "write" role)
    // For each user with roles:
    //   - Create global role mappings
}
```

### Phase 10: Testing
**Files to create:**
- `gohookbridge/store/acl_test.go` - Update with new permission logic
- `gohookbridge/server/api_test.go` - Add tests for new endpoints
- `web/src/views/__tests__/AdminRBACView.test.ts` - Update tests
- `web/src/views/__tests__/ChannelDetailView.test.ts` - Add ACL tests

## Backward Compatibility

### Deprecated Fields
- `User.Roles` - Use RoleMapping instead
- `User.Channels` - Use ChannelRoleMapping instead

### Migration Path
1. Keep deprecated fields functional during transition
2. Auto-migrate existing data on first boot
3. UI should show both old and new data during migration

## Security Considerations

1. **OIDC Group Spoofing**: Validate groups claim comes from trusted OIDC provider
2. **Permission Escalation**: Only admins can create global role mappings
3. **Channel Owner Protection**: Channel owners cannot remove their own owner role
4. **Audit Logging**: Log all RBAC changes for compliance

## Testing Strategy

### Unit Tests
- Permission resolution with various role mappings
- OIDC group extraction
- Channel ACL operations

### Integration Tests
- End-to-end RBAC flow (create mapping → check permission)
- OIDC login with groups → channel access
- Channel creation → auto-owner mapping

### UI Tests
- Global RBAC management
- Channel ACL management
- Permission-based UI visibility

## Rollout Plan

1. **Phase 1-5**: Backend implementation (no breaking changes)
2. **Phase 6-8**: Frontend implementation
3. **Phase 9**: Migration (automatic on first boot)
4. **Phase 10**: Testing and validation

## Success Criteria

- [ ] Admin can create global role mappings for users and groups
- [ ] Admin can configure OIDC groups claim per provider
- [ ] Channel creator automatically gets owner role
- [ ] Channel owners can manage channel ACL
- [ ] Permission resolution works for users and OIDC groups
- [ ] Backward compatibility maintained for existing users
- [ ] All tests pass (unit, integration, UI)
