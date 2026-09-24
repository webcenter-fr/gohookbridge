package repository

import (
	"context"
	"fmt"
	"os"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
)

// MigrateRBAC migrates legacy user.roles/user.channels assignments into the
// RBAC role-mapping and channel-ACL model. It runs exactly once, guarded by
// the /meta/rbac_migrated flag in the FSM.
func (rs *RaftStore) MigrateRBAC(ctx context.Context) error {
	migrated, err := getFSMValue(rs.db, "/meta/rbac_migrated")
	if err == nil && migrated != nil {
		return nil
	}

	users, err := rs.ListUsers(ctx)
	if err != nil {
		return fmt.Errorf("list users for migration: %w", err)
	}
	if len(users) == 0 {
		return nil
	}

	for _, u := range users {
		for _, roleName := range u.Roles {
			m := &domain.RoleMapping{
				Type:         "user",
				Subject:      u.ID,
				Role:         roleName,
				ChannelScope: "*",
			}
			if err := rs.CreateRoleMapping(ctx, m); err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: rbac migration: create role mapping for user %s: %v\n", u.ID, err)
			}
		}
		for _, ch := range u.Channels {
			if ch == "*" {
				continue
			}
			m := &domain.ChannelRoleMapping{
				ChannelID: ch,
				Type:      "user",
				Subject:   u.ID,
				Role:      "write",
			}
			if err := rs.CreateChannelRoleMapping(ctx, m); err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: rbac migration: create channel role mapping for user %s, channel %s: %v\n", u.ID, ch, err)
			}
		}
	}

	if err := rs.applyCommand("set", "/meta/rbac_migrated", []byte("true")); err != nil {
		return fmt.Errorf("mark migration complete: %w", err)
	}

	return nil
}
