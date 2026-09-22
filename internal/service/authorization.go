package service

import (
	"context"
	"strings"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
)

func (s *Service) getUserObject(ctx context.Context, username string) *domain.User {
	user, err := s.repo.GetUserByUsername(ctx, username)
	if err != nil {
		user2, err2 := s.repo.GetUser(ctx, username)
		if err2 != nil {
			return nil
		}
		user = user2
	}
	return user
}

func hasChannelAccessList(userChannels []string, channelID string) bool {
	for _, ch := range userChannels {
		if ch == "*" || ch == channelID {
			return true
		}
	}
	return false
}

func (s *Service) checkGlobalRolePermissions(ctx context.Context, user *domain.User, perm domain.Permission, channelID string) bool {
	for _, roleName := range user.Roles {
		role, err := s.repo.GetRole(ctx, roleName)
		if err != nil {
			continue
		}
		for _, p := range role.Permissions {
			if p == string(perm) || p == "*" {
				if strings.HasPrefix(string(perm), "channel:") && channelID != "" {
					if hasChannelAccessList(user.Channels, channelID) {
						return true
					}
					continue
				}
				if p == "*" && channelID != "" {
					if hasChannelAccessList(user.Channels, channelID) {
						return true
					}
					continue
				}
				return true
			}
		}
	}
	return false
}

func (s *Service) checkRoleMappingPermissions(ctx context.Context, user *domain.User, perm domain.Permission, channelID string, oidcGroups []string) bool {
	mappings, err := s.repo.ListRoleMappings(ctx)
	if err != nil {
		return false
	}
	for _, m := range mappings {
		if m.Type == "user" && m.Subject != user.ID && m.Subject != user.Username {
			continue
		}
		if m.Type == "group" {
			matched := false
			for _, g := range oidcGroups {
				if g == m.Subject {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		role, err := s.repo.GetRole(ctx, m.Role)
		if err != nil {
			continue
		}
		for _, p := range role.Permissions {
			if p == string(perm) || p == "*" {
				if m.ChannelScope == "*" || m.ChannelScope == channelID {
					return true
				}
				if channelID == "" && (p == string(perm) || p == "*") {
					return true
				}
			}
		}
	}
	return false
}

func (s *Service) checkChannelRoleMappingPermissions(ctx context.Context, user *domain.User, perm domain.Permission, channelID string, oidcGroups []string) bool {
	acls, err := s.repo.ListChannelRoleMappings(ctx, channelID)
	if err != nil {
		return false
	}
	roleLevel := func(role string) int {
		switch role {
		case "owner":
			return 3
		case "write":
			return 2
		case "read":
			return 1
		default:
			return 0
		}
	}
	requiredLevel := 1
	if perm == domain.PermChannelWrite {
		requiredLevel = 2
	}
	for _, a := range acls {
		if a.Type == "user" && a.Subject != user.ID && a.Subject != user.Username {
			continue
		}
		if a.Type == "group" {
			matched := false
			for _, g := range oidcGroups {
				if g == a.Subject {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if roleLevel(a.Role) >= requiredLevel {
			return true
		}
	}
	return false
}

func (s *Service) isChannelCreator(ctx context.Context, userID string, channelID string) bool {
	ch, err := s.repo.GetChannel(ctx, channelID)
	if err != nil {
		return false
	}
	return ch.CreatedBy == userID
}

func (s *Service) UserHasPermission(ctx context.Context, username string, perm domain.Permission, channelID string) bool {
	user := s.getUserObject(ctx, username)
	if user == nil {
		return false
	}

	oidcGroups := user.OIDCSubjects

	if s.checkGlobalRolePermissions(ctx, user, perm, channelID) {
		return true
	}

	if s.checkRoleMappingPermissions(ctx, user, perm, channelID, oidcGroups) {
		return true
	}

	if channelID != "" && strings.HasPrefix(string(perm), "channel:") {
		if perm == domain.PermChannelWrite || perm == domain.PermChannelRead {
			if s.checkChannelRoleMappingPermissions(ctx, user, perm, channelID, oidcGroups) {
				return true
			}
		}

		if s.isChannelCreator(ctx, username, channelID) {
			return true
		}
	}

	return false
}

func (s *Service) UserChannels(ctx context.Context, username string) ([]string, error) {
	user := s.getUserObject(ctx, username)
	if user == nil {
		return []string{}, nil
	}

	oidcGroups := user.OIDCSubjects

	allChannels := func() ([]string, error) {
		chs, err := s.repo.ListChannels(ctx)
		if err != nil {
			return nil, err
		}
		ids := make([]string, len(chs))
		for i, ch := range chs {
			ids[i] = ch.ID
		}
		return ids, nil
	}

	for _, roleName := range user.Roles {
		role, err := s.repo.GetRole(ctx, roleName)
		if err != nil {
			continue
		}
		for _, p := range role.Permissions {
			if p == "*" {
				return allChannels()
			}
		}
	}

	mappings, err := s.repo.ListRoleMappings(ctx)
	if err == nil {
		for _, m := range mappings {
			if m.Type == "user" && (m.Subject == user.ID || m.Subject == user.Username) {
				role, err := s.repo.GetRole(ctx, m.Role)
				if err == nil {
					for _, p := range role.Permissions {
						if p == "*" {
							return allChannels()
						}
					}
				}
			}
			if m.Type == "group" {
				matched := false
				for _, g := range oidcGroups {
					if g == m.Subject {
						matched = true
						break
					}
				}
				if matched {
					role, err := s.repo.GetRole(ctx, m.Role)
					if err == nil {
						for _, p := range role.Permissions {
							if p == "*" {
								return allChannels()
							}
						}
					}
				}
			}
		}
	}

	channelSet := make(map[string]bool)
	for _, ch := range user.Channels {
		channelSet[ch] = true
	}

	channels, err := s.repo.ListChannels(ctx)
	if err == nil {
		for _, ch := range channels {
			if s.isChannelCreator(ctx, username, ch.ID) {
				channelSet[ch.ID] = true
			}
			acls, err := s.repo.ListChannelRoleMappings(ctx, ch.ID)
			if err != nil {
				continue
			}
			for _, a := range acls {
				if a.Type == "user" && (a.Subject == user.ID || a.Subject == user.Username) {
					channelSet[ch.ID] = true
				}
				if a.Type == "group" {
					for _, g := range oidcGroups {
						if g == a.Subject {
							channelSet[ch.ID] = true
						}
					}
				}
			}
		}
	}

	result := make([]string, 0, len(channelSet))
	for ch := range channelSet {
		result = append(result, ch)
	}
	return result, nil
}

func (s *Service) IsAdmin(ctx context.Context, username string) bool {
	return s.UserHasPermission(ctx, username, "*", "")
}

// HasChannelAccess reports whether the user's channel list grants access to
// the given channel (or wildcard).
func HasChannelAccess(userChannels []string, channelID string) bool {
	return hasChannelAccessList(userChannels, channelID)
}

func (s *Service) GetUserPermissions(ctx context.Context, username string) []string {
	perms := make(map[string]bool)
	user := s.getUserObject(ctx, username)
	if user == nil {
		return []string{}
	}

	for _, roleName := range user.Roles {
		role, err := s.repo.GetRole(ctx, roleName)
		if err != nil {
			continue
		}
		for _, p := range role.Permissions {
			perms[p] = true
		}
	}

	mappings, err := s.repo.ListRoleMappings(ctx)
	if err == nil {
		oidcGroups := user.OIDCSubjects
		for _, m := range mappings {
			if m.Type == "user" && m.Subject != user.ID && m.Subject != user.Username {
				continue
			}
			if m.Type == "group" {
				matched := false
				for _, g := range oidcGroups {
					if g == m.Subject {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			role, err := s.repo.GetRole(ctx, m.Role)
			if err != nil {
				continue
			}
			for _, p := range role.Permissions {
				perms[p] = true
			}
		}
	}

	result := make([]string, 0, len(perms))
	for p := range perms {
		result = append(result, p)
	}
	return result
}

func (s *Service) HasChannelRole(ctx context.Context, username string, channelID string, role string) bool {
	user := s.getUserObject(ctx, username)
	if user == nil {
		return false
	}
	oidcGroups := user.OIDCSubjects
	roleLevel := func(r string) int {
		switch r {
		case "owner":
			return 3
		case "write":
			return 2
		case "read":
			return 1
		default:
			return 0
		}
	}
	requiredLevel := roleLevel(role)

	if s.isChannelCreator(ctx, username, channelID) && requiredLevel <= 3 {
		return true
	}

	acls, err := s.repo.ListChannelRoleMappings(ctx, channelID)
	if err != nil {
		return false
	}
	for _, a := range acls {
		if a.Type == "user" && a.Subject != user.ID && a.Subject != user.Username {
			continue
		}
		if a.Type == "group" {
			matched := false
			for _, g := range oidcGroups {
				if g == a.Subject {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if roleLevel(a.Role) >= requiredLevel {
			return true
		}
	}
	return false
}
