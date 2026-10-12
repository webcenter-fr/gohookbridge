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

func principal(t *testing.T, svc *Service, username string) *domain.User {
	t.Helper()
	u, err := svc.GetUserByUsername(context.Background(), username)
	assert.NilError(t, err)
	return u
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
	admin := principal(t, svc, "admin1")

	assert.Assert(t, svc.UserHasPermission(ctx, admin, nil, "*", ""))
	assert.Assert(t, svc.UserHasPermission(ctx, admin, nil, "global:read", ""))
	assert.Assert(t, svc.UserHasPermission(ctx, admin, nil, "channel:write", "proj1"))
	assert.Assert(t, svc.UserHasPermission(ctx, admin, nil, "nonexistent:perm", ""))
}

func TestUserHasPermission_GlobalWrite(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()
	user := principal(t, svc, "projectadmin")

	assert.Assert(t, !svc.UserHasPermission(ctx, user, nil, "global:read", ""))
	assert.Assert(t, !svc.UserHasPermission(ctx, user, nil, "global:write", ""))
	assert.Assert(t, !svc.UserHasPermission(ctx, user, nil, "users:read", ""))

	assert.Assert(t, svc.UserHasPermission(ctx, user, nil, "channel:write", "proj1"))
	assert.Assert(t, svc.UserHasPermission(ctx, user, nil, "channel:read", "proj1"))
}

func TestUserHasPermission_ProjectScope(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()
	user := principal(t, svc, "scopeduser")

	assert.Assert(t, svc.UserHasPermission(ctx, user, nil, "channel:read", "proj1"))
	assert.Assert(t, !svc.UserHasPermission(ctx, user, nil, "channel:read", "proj2"))
	assert.Assert(t, !svc.UserHasPermission(ctx, user, nil, "channel:write", "proj1"))
	assert.Assert(t, !svc.UserHasPermission(ctx, user, nil, "global:read", ""))
}

func TestUserHasPermission_StarProjects(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()
	user := principal(t, svc, "staruser")

	assert.Assert(t, svc.UserHasPermission(ctx, user, nil, "channel:read", "any-project"))
	assert.Assert(t, svc.UserHasPermission(ctx, user, nil, "channel:read", ""))
}

func TestUserHasPermission_UnknownUser(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.Assert(t, !svc.UserHasPermission(ctx, nil, nil, "channel:read", "proj1"))
	assert.Assert(t, !svc.UserHasPermission(ctx, nil, nil, "*", ""))
}

func TestGetUserPermissions(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	perms := svc.GetUserPermissions(ctx, principal(t, svc, "admin1"), nil)
	assert.Equal(t, len(perms), 1)
	assert.Equal(t, perms[0], "*")

	perms = svc.GetUserPermissions(ctx, principal(t, svc, "projectadmin"), nil)
	assert.Equal(t, len(perms), 2)
	assert.Assert(t, contains(perms, "channel:read"), "expected channel:read")
	assert.Assert(t, contains(perms, "channel:write"), "expected channel:write")

	perms = svc.GetUserPermissions(ctx, nil, nil)
	assert.Assert(t, len(perms) == 0)
}

func TestIsAdmin(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.Assert(t, svc.IsAdmin(ctx, principal(t, svc, "admin1"), nil))
	assert.Assert(t, !svc.IsAdmin(ctx, principal(t, svc, "projectadmin"), nil))
	assert.Assert(t, !svc.IsAdmin(ctx, principal(t, svc, "scopeduser"), nil))
	assert.Assert(t, !svc.IsAdmin(ctx, principal(t, svc, "viewer1"), nil))
	assert.Assert(t, !svc.IsAdmin(ctx, nil, nil))
}

func TestUserHasPermission_ChannelRoleMapping(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()
	viewer := principal(t, svc, "viewer1")

	assert.NilError(t, svc.CreateChannel(ctx, &domain.Channel{ID: "proj1", CreatedBy: "admin1"}))
	assert.NilError(t, svc.CreateChannelRoleMapping(ctx, &domain.ChannelRoleMapping{
		ChannelID: "proj1",
		Type:      "user",
		Subject:   "viewer1",
		Role:      "read",
	}))

	assert.Assert(t, svc.UserHasPermission(ctx, viewer, nil, "channel:read", "proj1"))
	assert.Assert(t, !svc.UserHasPermission(ctx, viewer, nil, "channel:write", "proj1"))

	assert.NilError(t, svc.CreateChannelRoleMapping(ctx, &domain.ChannelRoleMapping{
		ChannelID: "proj1",
		Type:      "user",
		Subject:   "viewer1",
		Role:      "write",
	}))
	assert.Assert(t, svc.UserHasPermission(ctx, viewer, nil, "channel:write", "proj1"))
}

func TestHasChannelRole(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()
	viewer := principal(t, svc, "viewer1")

	assert.NilError(t, svc.CreateChannel(ctx, &domain.Channel{ID: "proj1"}))
	assert.NilError(t, svc.CreateChannelRoleMapping(ctx, &domain.ChannelRoleMapping{
		ChannelID: "proj1",
		Type:      "user",
		Subject:   "viewer1",
		Role:      "owner",
	}))

	assert.Assert(t, svc.HasChannelRole(ctx, viewer, nil, "proj1", "owner"))
	assert.Assert(t, svc.HasChannelRole(ctx, viewer, nil, "proj1", "write"))
	assert.Assert(t, !svc.HasChannelRole(ctx, principal(t, svc, "admin1"), nil, "proj1", "owner"))
}

// F03: OIDC authorization binds to the exact (provider, issuer, subject)
// external identity; email and legacy oidc_subjects never resolve.
func TestResolvePrincipalExternalIdentity(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	linked := &domain.User{
		ID:       "oidcuser",
		Username: "oidcuser",
		Roles:    []string{"channel_viewer"},
		ExternalIdentities: []domain.ExternalIdentity{
			{ProviderID: "okta", Issuer: "https://idp.example.com", Subject: "sub-123"},
		},
	}
	assert.NilError(t, svc.CreateUser(ctx, linked))

	tok := &SessionToken{Method: "oidc", Provider: "okta", Issuer: "https://idp.example.com/", Subject: "sub-123"}
	got := svc.ResolvePrincipal(ctx, tok)
	assert.Assert(t, got != nil)
	assert.Equal(t, got.ID, "oidcuser")

	// Different subject must not resolve (identity collision).
	assert.Assert(t, svc.ResolvePrincipal(ctx, &SessionToken{
		Method: "oidc", Provider: "okta", Issuer: "https://idp.example.com", Subject: "sub-999",
	}) == nil)

	// Different issuer must not resolve.
	assert.Assert(t, svc.ResolvePrincipal(ctx, &SessionToken{
		Method: "oidc", Provider: "okta", Issuer: "https://evil.example.com", Subject: "sub-123",
	}) == nil)
}

// F34: groups come only from the verified session, never from user.OIDCSubjects.
func TestGroupsFromVerifiedClaims(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	// A legacy oidc_subjects value that happens to equal a group must NOT grant.
	legacy := &domain.User{
		ID:           "legacy",
		Username:     "legacy",
		OIDCSubjects: []string{"admins"},
	}
	assert.NilError(t, svc.CreateUser(ctx, legacy))
	assert.NilError(t, svc.CreateRoleMapping(ctx, &domain.RoleMapping{
		ID: "grp-admin", Type: "group", Subject: "admins", Role: "admin", ChannelScope: "*",
	}))

	assert.Assert(t, !svc.UserHasPermission(ctx, principal(t, svc, "legacy"), nil, "*", ""))
	assert.Assert(t, svc.UserHasPermission(ctx, principal(t, svc, "legacy"), []string{"admins"}, "*", ""))
}

// F02: a channel-scoped mapping must not grant collection/object access to a
// different channel.
func TestScopedChannelPermission(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	scoped := &domain.User{ID: "scoped", Username: "scoped"}
	assert.NilError(t, svc.CreateUser(ctx, scoped))
	assert.NilError(t, svc.CreateRoleMapping(ctx, &domain.RoleMapping{
		ID: "m1", Type: "user", Subject: "scoped", Role: "channel_admin", ChannelScope: "proj1",
	}))

	user := principal(t, svc, "scoped")
	assert.Assert(t, svc.UserHasPermission(ctx, user, nil, "channel:write", "proj1"))
	assert.Assert(t, !svc.UserHasPermission(ctx, user, nil, "channel:write", "proj2"))
	// Collection access (channelID == "") must require a wildcard scope.
	assert.Assert(t, !svc.UserHasPermission(ctx, user, nil, "channel:read", ""))
}

func TestCanAssignRoles(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.Assert(t, svc.CanAssignRoles(ctx, principal(t, svc, "admin1"), nil, []string{"admin"}, "*"))
	assert.Assert(t, !svc.CanAssignRoles(ctx, principal(t, svc, "projectadmin"), nil, []string{"admin"}, "*"))
	assert.Assert(t, svc.CanAssignRoles(ctx, principal(t, svc, "projectadmin"), nil, []string{"channel_viewer"}, "proj1"))
	assert.Assert(t, !svc.CanAssignRoles(ctx, principal(t, svc, "projectadmin"), nil, []string{"channel_viewer"}, "other"))
}
