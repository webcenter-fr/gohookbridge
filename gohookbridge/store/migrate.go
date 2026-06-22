package store

import (
	"fmt"
	"os"
)

func MigrateRBAC(rs *RaftStore) error {
	migrated, err := getFSMValue(rs.db, "/meta/rbac_migrated")
	if err == nil && migrated != nil {
		return nil
	}

	users, err := rs.ListUsers()
	if err != nil {
		return fmt.Errorf("list users for migration: %w", err)
	}
	if len(users) == 0 {
		return nil
	}

	for _, u := range users {
		for _, roleName := range u.Roles {
			m := &RoleMapping{
				Type:         "user",
				Subject:      u.ID,
				Role:         roleName,
				ChannelScope: "*",
			}
			if err := rs.CreateRoleMapping(m); err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: rbac migration: create role mapping for user %s: %v\n", u.ID, err)
			}
		}
		for _, ch := range u.Channels {
			if ch == "*" {
				continue
			}
			m := &ChannelRoleMapping{
				ChannelID: ch,
				Type:      "user",
				Subject:   u.ID,
				Role:      "write",
			}
			if err := rs.CreateChannelRoleMapping(m); err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: rbac migration: create channel role mapping for user %s, channel %s: %v\n", u.ID, ch, err)
			}
		}
	}

	if err := rs.applyCommand("set", "/meta/rbac_migrated", []byte("true")); err != nil {
		return fmt.Errorf("mark migration complete: %w", err)
	}

	return nil
}