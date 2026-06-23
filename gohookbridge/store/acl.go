package store

import (
	"context"
	"net/http"
	"strings"
)

const UsernameContextKey = "username"
const GroupsContextKey = "oidc_groups"

type contextKey string

const contextKeyChannelID contextKey = "channel_id"

func GetUsernameFromContext(ctx context.Context) string {
	username, _ := ctx.Value(UsernameContextKey).(string)
	return username
}

func GetGroupsFromContext(ctx context.Context) []string {
	groups, _ := ctx.Value(GroupsContextKey).([]string)
	return groups
}

func RequirePermission(rs *RaftStore, perm Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			username := GetUsernameFromContext(r.Context())
			if username == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			channelID := ""
			if pid, ok := r.Context().Value(contextKeyChannelID).(string); ok {
				channelID = pid
			}

			if !UserHasPermission(rs, username, perm, channelID) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func RequireChannelACLPermission(rs *RaftStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			username := GetUsernameFromContext(r.Context())
			if username == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			channelID := ""
			if pid, ok := r.Context().Value(contextKeyChannelID).(string); ok {
				channelID = pid
			}

			if UserHasPermission(rs, username, PermChannelWrite, channelID) {
				next.ServeHTTP(w, r)
				return
			}

			if UserHasPermission(rs, username, PermRBACWrite, channelID) {
				next.ServeHTTP(w, r)
				return
			}

			if UserHasPermission(rs, username, PermAll, channelID) {
				next.ServeHTTP(w, r)
				return
			}

			http.Error(w, "Forbidden", http.StatusForbidden)
		})
	}
}

func getUserObject(rs *RaftStore, username string) *User {
	user, err := rs.GetUserByUsername(username)
	if err != nil {
		user2, err2 := rs.GetUser(username)
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

func checkGlobalRolePermissions(rs *RaftStore, user *User, perm Permission, channelID string) bool {
	for _, roleName := range user.Roles {
		role, err := rs.GetRole(roleName)
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

func checkRoleMappingPermissions(rs *RaftStore, user *User, perm Permission, channelID string, oidcGroups []string) bool {
	mappings, err := rs.ListRoleMappings()
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
		role, err := rs.GetRole(m.Role)
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

func checkChannelRoleMappingPermissions(rs *RaftStore, user *User, perm Permission, channelID string, oidcGroups []string) bool {
	acls, err := rs.ListChannelRoleMappings(channelID)
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
	if perm == PermChannelWrite {
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

func isChannelCreator(rs *RaftStore, userID string, channelID string) bool {
	ch, err := rs.GetChannel(channelID)
	if err != nil {
		return false
	}
	return ch.CreatedBy == userID
}

func UserHasPermission(rs *RaftStore, username string, perm Permission, channelID string) bool {
	user := getUserObject(rs, username)
	if user == nil {
		return false
	}

	oidcGroups := user.OIDCSubjects

	if checkGlobalRolePermissions(rs, user, perm, channelID) {
		return true
	}

	if checkRoleMappingPermissions(rs, user, perm, channelID, oidcGroups) {
		return true
	}

	if channelID != "" && strings.HasPrefix(string(perm), "channel:") {
		if perm == PermChannelWrite || perm == PermChannelRead {
			if checkChannelRoleMappingPermissions(rs, user, perm, channelID, oidcGroups) {
				return true
			}
		}

		if isChannelCreator(rs, username, channelID) {
			return true
		}
	}

	return false
}

func UserChannels(rs *RaftStore, username string) ([]string, error) {
	user := getUserObject(rs, username)
	if user == nil {
		return []string{}, nil
	}

	oidcGroups := user.OIDCSubjects

	for _, roleName := range user.Roles {
		role, err := rs.GetRole(roleName)
		if err != nil {
			continue
		}
		for _, p := range role.Permissions {
			if p == "*" {
				chs, _ := rs.ListChannels()
				ids := make([]string, len(chs))
				for i, ch := range chs {
					ids[i] = ch.ID
				}
				return ids, nil
			}
		}
	}

	mappings, err := rs.ListRoleMappings()
	if err == nil {
		for _, m := range mappings {
			if m.Type == "user" && (m.Subject == user.ID || m.Subject == user.Username) {
				role, err := rs.GetRole(m.Role)
				if err == nil {
					for _, p := range role.Permissions {
						if p == "*" {
							chs, _ := rs.ListChannels()
							ids := make([]string, len(chs))
							for i, ch := range chs {
								ids[i] = ch.ID
							}
							return ids, nil
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
					role, err := rs.GetRole(m.Role)
					if err == nil {
						for _, p := range role.Permissions {
							if p == "*" {
								chs, _ := rs.ListChannels()
								ids := make([]string, len(chs))
								for i, ch := range chs {
									ids[i] = ch.ID
								}
								return ids, nil
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

	channels, err := rs.ListChannels()
	if err == nil {
		for _, ch := range channels {
			if isChannelCreator(rs, username, ch.ID) {
				channelSet[ch.ID] = true
			}
			acls, err := rs.ListChannelRoleMappings(ch.ID)
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

func IsAdmin(rs *RaftStore, username string) bool {
	return UserHasPermission(rs, username, "*", "")
}

func hasChannelAccess(userChannels []string, channelID string) bool {
	return hasChannelAccessList(userChannels, channelID)
}

func GetUserPermissions(rs *RaftStore, username string) []string {
	perms := make(map[string]bool)
	user := getUserObject(rs, username)
	if user == nil {
		return []string{}
	}

	for _, roleName := range user.Roles {
		role, err := rs.GetRole(roleName)
		if err != nil {
			continue
		}
		for _, p := range role.Permissions {
			perms[p] = true
		}
	}

	mappings, err := rs.ListRoleMappings()
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
			role, err := rs.GetRole(m.Role)
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
func HasChannelRole(rs *RaftStore, username string, channelID string, role string) bool {
	user := getUserObject(rs, username)
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

	if isChannelCreator(rs, username, channelID) && requiredLevel <= 3 {
		return true
	}

	acls, err := rs.ListChannelRoleMappings(channelID)
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
