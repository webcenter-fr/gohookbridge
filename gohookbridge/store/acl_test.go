package store

import (
	"testing"

	"gotest.tools/v3/assert"
)

func setupUsersWithRoles(t *testing.T) *RaftStore {
	t.Helper()
	rs := newTestRaftStore(t)

	err := rs.CreateUser(&User{
		ID:       "admin1",
		Username: "admin1",
		Roles:    []string{"admin"},
		Channels: []string{"*"},
	})
	assert.NilError(t, err)

	err = rs.CreateUser(&User{
		ID:       "projectadmin",
		Username: "projectadmin",
		Roles:    []string{"channel_admin"},
		Channels: []string{"proj1", "proj2"},
	})
	assert.NilError(t, err)

	err = rs.CreateUser(&User{
		ID:       "scopeduser",
		Username: "scopeduser",
		Roles:    []string{"channel_viewer"},
		Channels: []string{"proj1"},
	})
	assert.NilError(t, err)

	err = rs.CreateUser(&User{
		ID:       "staruser",
		Username: "staruser",
		Roles:    []string{"channel_viewer"},
		Channels: []string{"*"},
	})
	assert.NilError(t, err)

	err = rs.CreateUser(&User{
		ID:       "viewer1",
		Username: "viewer1",
		Roles:    []string{"channel_viewer"},
		Channels: []string{"proj1"},
	})
	assert.NilError(t, err)

	return rs
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
	rs := setupUsersWithRoles(t)

	assert.Assert(t, UserHasPermission(rs, "admin1", "*", ""))
	assert.Assert(t, UserHasPermission(rs, "admin1", "global:read", ""))
	assert.Assert(t, UserHasPermission(rs, "admin1", "channel:write", "proj1"))
	assert.Assert(t, UserHasPermission(rs, "admin1", "nonexistent:perm", ""))
}

func TestUserHasPermission_GlobalWrite(t *testing.T) {
	rs := setupUsersWithRoles(t)

	assert.Assert(t, !UserHasPermission(rs, "projectadmin", "global:read", ""))
	assert.Assert(t, !UserHasPermission(rs, "projectadmin", "global:write", ""))
	assert.Assert(t, !UserHasPermission(rs, "projectadmin", "users:read", ""))

	assert.Assert(t, UserHasPermission(rs, "projectadmin", "channel:write", "proj1"))
	assert.Assert(t, UserHasPermission(rs, "projectadmin", "channel:read", "proj1"))
}

func TestUserHasPermission_ProjectScope(t *testing.T) {
	rs := setupUsersWithRoles(t)

	assert.Assert(t, UserHasPermission(rs, "scopeduser", "channel:read", "proj1"))
	assert.Assert(t, !UserHasPermission(rs, "scopeduser", "channel:read", "proj2"))
	assert.Assert(t, !UserHasPermission(rs, "scopeduser", "channel:write", "proj1"))
	assert.Assert(t, !UserHasPermission(rs, "scopeduser", "global:read", ""))
}

func TestUserHasPermission_StarProjects(t *testing.T) {
	rs := setupUsersWithRoles(t)

	assert.Assert(t, UserHasPermission(rs, "staruser", "channel:read", "any-project"))
	assert.Assert(t, UserHasPermission(rs, "staruser", "channel:read", ""))
}

func TestUserHasPermission_UnknownUser(t *testing.T) {
	rs := setupUsersWithRoles(t)

	assert.Assert(t, !UserHasPermission(rs, "unknown", "channel:read", "proj1"))
	assert.Assert(t, !UserHasPermission(rs, "unknown", "*", ""))
}

func TestGetUserPermissions(t *testing.T) {
	rs := setupUsersWithRoles(t)

	perms := GetUserPermissions(rs, "admin1")
	assert.Equal(t, len(perms), 1)
	assert.Equal(t, perms[0], "*")

	perms = GetUserPermissions(rs, "projectadmin")
	assert.Equal(t, len(perms), 2)
	assert.Assert(t, contains(perms, "channel:read"), "expected project:read")
	assert.Assert(t, contains(perms, "channel:write"), "expected project:write")

	perms = GetUserPermissions(rs, "unknown")
	assert.Assert(t, len(perms) == 0)
}

func TestIsAdmin(t *testing.T) {
	rs := setupUsersWithRoles(t)

	assert.Assert(t, IsAdmin(rs, "admin1"))
	assert.Assert(t, !IsAdmin(rs, "projectadmin"))
	assert.Assert(t, !IsAdmin(rs, "scopeduser"))
	assert.Assert(t, !IsAdmin(rs, "viewer1"))
	assert.Assert(t, !IsAdmin(rs, "unknown"))
}