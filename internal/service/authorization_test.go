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

	assert.Assert(t, svc.UserHasPermission(ctx, "admin1", "*", ""))
	assert.Assert(t, svc.UserHasPermission(ctx, "admin1", "global:read", ""))
	assert.Assert(t, svc.UserHasPermission(ctx, "admin1", "channel:write", "proj1"))
	assert.Assert(t, svc.UserHasPermission(ctx, "admin1", "nonexistent:perm", ""))
}

func TestUserHasPermission_GlobalWrite(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.Assert(t, !svc.UserHasPermission(ctx, "projectadmin", "global:read", ""))
	assert.Assert(t, !svc.UserHasPermission(ctx, "projectadmin", "global:write", ""))
	assert.Assert(t, !svc.UserHasPermission(ctx, "projectadmin", "users:read", ""))

	assert.Assert(t, svc.UserHasPermission(ctx, "projectadmin", "channel:write", "proj1"))
	assert.Assert(t, svc.UserHasPermission(ctx, "projectadmin", "channel:read", "proj1"))
}

func TestUserHasPermission_ProjectScope(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.Assert(t, svc.UserHasPermission(ctx, "scopeduser", "channel:read", "proj1"))
	assert.Assert(t, !svc.UserHasPermission(ctx, "scopeduser", "channel:read", "proj2"))
	assert.Assert(t, !svc.UserHasPermission(ctx, "scopeduser", "channel:write", "proj1"))
	assert.Assert(t, !svc.UserHasPermission(ctx, "scopeduser", "global:read", ""))
}

func TestUserHasPermission_StarProjects(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.Assert(t, svc.UserHasPermission(ctx, "staruser", "channel:read", "any-project"))
	assert.Assert(t, svc.UserHasPermission(ctx, "staruser", "channel:read", ""))
}

func TestUserHasPermission_UnknownUser(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.Assert(t, !svc.UserHasPermission(ctx, "unknown", "channel:read", "proj1"))
	assert.Assert(t, !svc.UserHasPermission(ctx, "unknown", "*", ""))
}

func TestGetUserPermissions(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	perms := svc.GetUserPermissions(ctx, "admin1")
	assert.Equal(t, len(perms), 1)
	assert.Equal(t, perms[0], "*")

	perms = svc.GetUserPermissions(ctx, "projectadmin")
	assert.Equal(t, len(perms), 2)
	assert.Assert(t, contains(perms, "channel:read"), "expected project:read")
	assert.Assert(t, contains(perms, "channel:write"), "expected project:write")

	perms = svc.GetUserPermissions(ctx, "unknown")
	assert.Assert(t, len(perms) == 0)
}

func TestIsAdmin(t *testing.T) {
	svc := setupUsersWithRoles(t)
	ctx := context.Background()

	assert.Assert(t, svc.IsAdmin(ctx, "admin1"))
	assert.Assert(t, !svc.IsAdmin(ctx, "projectadmin"))
	assert.Assert(t, !svc.IsAdmin(ctx, "scopeduser"))
	assert.Assert(t, !svc.IsAdmin(ctx, "viewer1"))
	assert.Assert(t, !svc.IsAdmin(ctx, "unknown"))
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

	assert.Assert(t, svc.UserHasPermission(ctx, "viewer1", "channel:read", "proj1"))
	assert.Assert(t, !svc.UserHasPermission(ctx, "viewer1", "channel:write", "proj1"))

	assert.NilError(t, svc.CreateChannelRoleMapping(ctx, &domain.ChannelRoleMapping{
		ChannelID: "proj1",
		Type:      "user",
		Subject:   "viewer1",
		Role:      "write",
	}))
	assert.Assert(t, svc.UserHasPermission(ctx, "viewer1", "channel:write", "proj1"))
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

	assert.Assert(t, svc.HasChannelRole(ctx, "viewer1", "proj1", "owner"))
	assert.Assert(t, svc.HasChannelRole(ctx, "viewer1", "proj1", "write"))
	assert.Assert(t, !svc.HasChannelRole(ctx, "admin1", "proj1", "owner"))
}
