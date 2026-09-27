package service

import (
	"context"
	"testing"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"gotest.tools/v3/assert"
)

func setupUsersWithRoles(t *testing.T) *Service {
	t.Helper()
	fake := newFakeRepository()
	svc := NewService(fake, nil)
	ctx := context.Background()

	for _, u := range []*domain.User{
		{ID: "admin1", Username: "admin1", Roles: []string{"admin"}, Channels: []string{"*"}},
		{ID: "projectadmin", Username: "projectadmin", Roles: []string{"channel_admin"}, Channels: []string{"proj1", "proj2"}},
		{ID: "scopeduser", Username: "scopeduser", Roles: []string{"channel_viewer"}, Channels: []string{"proj1"}},
		{ID: "staruser", Username: "staruser", Roles: []string{"channel_viewer"}, Channels: []string{"*"}},
		{ID: "viewer1", Username: "viewer1", Roles: []string{"channel_viewer"}, Channels: []string{"proj1"}},
	} {
		assert.NilError(t, svc.CreateUser(ctx, u))
	}
	return svc
}

// setupBareService returns a service on an empty fake repository plus a user
// with no direct role assignments.
func setupBareService(t *testing.T) (*Service, *domain.User) {
	t.Helper()
	svc := NewService(newFakeRepository(), nil)
	u := &domain.User{ID: "bare", Username: "bare", Roles: []string{}, Channels: []string{}}
	assert.NilError(t, svc.CreateUser(context.Background(), u))
	return svc, u
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func TestUserHasPermission_Admin_Wildcard(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.Assert(t, svc.UserHasPermission(ctx, "admin1", "*", "", nil))
	assert.Assert(t, svc.UserHasPermission(ctx, "admin1", "global:read", "", nil))
	assert.Assert(t, svc.UserHasPermission(ctx, "admin1", "channel:write", "proj1", nil))
	assert.Assert(t, svc.UserHasPermission(ctx, "admin1", "nonexistent:perm", "", nil))
}

func TestUserHasPermission_GlobalWrite(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.Assert(t, !svc.UserHasPermission(ctx, "projectadmin", "global:read", "", nil))
	assert.Assert(t, !svc.UserHasPermission(ctx, "projectadmin", "global:write", "", nil))
	assert.Assert(t, !svc.UserHasPermission(ctx, "projectadmin", "users:read", "", nil))

	assert.Assert(t, svc.UserHasPermission(ctx, "projectadmin", "channel:write", "proj1", nil))
	assert.Assert(t, svc.UserHasPermission(ctx, "projectadmin", "channel:read", "proj1", nil))
}

func TestUserHasPermission_ProjectScope(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.Assert(t, svc.UserHasPermission(ctx, "scopeduser", "channel:read", "proj1", nil))
	assert.Assert(t, !svc.UserHasPermission(ctx, "scopeduser", "channel:read", "proj2", nil))
	assert.Assert(t, !svc.UserHasPermission(ctx, "scopeduser", "channel:write", "proj1", nil))
	assert.Assert(t, !svc.UserHasPermission(ctx, "scopeduser", "global:read", "", nil))
}

func TestUserHasPermission_StarProjects(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.Assert(t, svc.UserHasPermission(ctx, "staruser", "channel:read", "any-project", nil))
	assert.Assert(t, svc.UserHasPermission(ctx, "staruser", "channel:read", "", nil))
}

func TestUserHasPermission_UnknownUser(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.Assert(t, !svc.UserHasPermission(ctx, "unknown", "channel:read", "proj1", nil))
	assert.Assert(t, !svc.UserHasPermission(ctx, "unknown", "*", "", nil))
}

func TestGetUserPermissions(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	perms := svc.GetUserPermissions(ctx, "admin1", nil)
	assert.Equal(t, len(perms), 1)
	assert.Equal(t, perms[0], "*")

	perms = svc.GetUserPermissions(ctx, "projectadmin", nil)
	assert.Equal(t, len(perms), 2)
	assert.Assert(t, contains(perms, "channel:read"), "expected project:read")
	assert.Assert(t, contains(perms, "channel:write"), "expected project:write")

	perms = svc.GetUserPermissions(ctx, "unknown", nil)
	assert.Assert(t, len(perms) == 0)
}

func TestIsAdmin(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.Assert(t, svc.IsAdmin(ctx, "admin1", nil))
	assert.Assert(t, !svc.IsAdmin(ctx, "projectadmin", nil))
	assert.Assert(t, !svc.IsAdmin(ctx, "scopeduser", nil))
	assert.Assert(t, !svc.IsAdmin(ctx, "viewer1", nil))
	assert.Assert(t, !svc.IsAdmin(ctx, "unknown", nil))
}

func TestUserHasPermission_ChannelRoleMapping(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.NilError(t, svc.CreateChannel(ctx, &domain.Channel{ID: "proj1", CreatedBy: "admin1"}))
	assert.NilError(t, svc.CreateChannelRoleMapping(ctx, &domain.ChannelRoleMapping{
		ChannelID: "proj1",
		Type:      "user",
		Subject:   "viewer1",
		Role:      "read",
	}))

	assert.Assert(t, svc.UserHasPermission(ctx, "viewer1", "channel:read", "proj1", nil))
	assert.Assert(t, !svc.UserHasPermission(ctx, "viewer1", "channel:write", "proj1", nil))

	assert.NilError(t, svc.CreateChannelRoleMapping(ctx, &domain.ChannelRoleMapping{
		ChannelID: "proj1",
		Type:      "user",
		Subject:   "viewer1",
		Role:      "write",
	}))
	assert.Assert(t, svc.UserHasPermission(ctx, "viewer1", "channel:write", "proj1", nil))
}

func TestHasChannelRole(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.NilError(t, svc.CreateChannel(ctx, &domain.Channel{ID: "proj1"}))
	assert.NilError(t, svc.CreateChannelRoleMapping(ctx, &domain.ChannelRoleMapping{
		ChannelID: "proj1",
		Type:      "user",
		Subject:   "viewer1",
		Role:      "owner",
	}))

	assert.Assert(t, svc.HasChannelRole(ctx, "viewer1", "proj1", "owner", nil))
	assert.Assert(t, svc.HasChannelRole(ctx, "viewer1", "proj1", "write", nil))
	assert.Assert(t, !svc.HasChannelRole(ctx, "admin1", "proj1", "owner", nil))
}

func TestRoleMappingScopeMatches(t *testing.T) {
	tests := []struct {
		name      string
		scope     string
		channelID string
		want      bool
	}{
		{"global scope matches global check", "*", "", true},
		{"global scope matches channel check", "*", "c1", true},
		{"scoped matches same channel", "c1", "c1", true},
		{"scoped does not match other channel", "c1", "c2", false},
		{"scoped never matches global check", "c1", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, roleMappingScopeMatches(tt.scope, tt.channelID), tt.want)
		})
	}
}

// SEC-001: a channel-scoped role mapping must never grant global permissions.
func TestUserHasPermission_RoleMappingChannelScope(t *testing.T) {
	tests := []struct {
		name       string
		scope      string
		wantGlobal bool
		wantProj1  bool
		wantProj2  bool
	}{
		{
			name:       "channel-scoped mapping stays channel-scoped",
			scope:      "proj1",
			wantGlobal: false,
			wantProj1:  true,
			wantProj2:  false,
		},
		{
			name:       "globally-scoped mapping grants global permissions",
			scope:      "*",
			wantGlobal: true,
			wantProj1:  true,
			wantProj2:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, u := setupBareService(t)
			ctx := context.Background()
			assert.NilError(t, svc.CreateRoleMapping(ctx, &domain.RoleMapping{
				Type:         "user",
				Subject:      u.Username,
				Role:         "admin",
				ChannelScope: tt.scope,
			}))

			assert.Equal(t, svc.UserHasPermission(ctx, u.Username, "*", "", nil), tt.wantGlobal)
			assert.Equal(t, svc.UserHasPermission(ctx, u.Username, "channel:write", "proj1", nil), tt.wantProj1)
			assert.Equal(t, svc.UserHasPermission(ctx, u.Username, "channel:write", "proj2", nil), tt.wantProj2)
			assert.Equal(t, svc.IsAdmin(ctx, u.Username, nil), tt.wantGlobal)
		})
	}
}

// SEC-003: group role mappings match on the session's OIDC groups claim.
func TestUserHasPermission_GroupMapping(t *testing.T) {
	svc, u := setupBareService(t)
	ctx := context.Background()
	assert.NilError(t, svc.CreateRoleMapping(ctx, &domain.RoleMapping{
		Type:         "group",
		Subject:      "eng",
		Role:         "channel_admin",
		ChannelScope: "*",
	}))

	assert.Assert(t, svc.UserHasPermission(ctx, u.Username, "channel:write", "proj1", []string{"eng"}))
	assert.Assert(t, !svc.UserHasPermission(ctx, u.Username, "channel:write", "proj1", []string{"other"}))
	assert.Assert(t, !svc.UserHasPermission(ctx, u.Username, "channel:write", "proj1", nil))
}

// SEC-003: group channel ACL mappings match on the session's OIDC groups claim.
func TestUserHasPermission_GroupChannelRoleMapping(t *testing.T) {
	svc, u := setupBareService(t)
	ctx := context.Background()
	assert.NilError(t, svc.CreateChannel(ctx, &domain.Channel{ID: "proj1"}))
	assert.NilError(t, svc.CreateChannelRoleMapping(ctx, &domain.ChannelRoleMapping{
		ChannelID: "proj1",
		Type:      "group",
		Subject:   "eng",
		Role:      "read",
	}))

	assert.Assert(t, svc.UserHasPermission(ctx, u.Username, "channel:read", "proj1", []string{"eng"}))
	assert.Assert(t, !svc.UserHasPermission(ctx, u.Username, "channel:write", "proj1", []string{"eng"}))
	assert.Assert(t, !svc.UserHasPermission(ctx, u.Username, "channel:read", "proj1", []string{"other"}))
}

// SEC-005: non-admin callers may only grant roles they themselves hold.
func TestCanGrantRole(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		granter string
		role    string
		want    bool
	}{
		{"admin grants admin", "admin1", "admin", true},
		{"channel_admin cannot grant admin", "projectadmin", "admin", false},
		{"channel_admin grants channel_viewer subset", "projectadmin", "channel_viewer", true},
		{"channel_admin grants channel_admin", "projectadmin", "channel_admin", true},
		{"unknown role is refused", "admin1", "does-not-exist", false},
		{"unknown granter is refused", "unknown", "channel_viewer", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, svc.CanGrantRole(ctx, tt.granter, tt.role, nil), tt.want)
		})
	}
}
