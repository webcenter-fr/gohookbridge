package store

import (
	"context"
	"net/http"
	"strings"
)

const UsernameContextKey = "username"

type contextKey string

const contextKeyChannelID contextKey = "channel_id"

func GetUsernameFromContext(ctx context.Context) string {
	username, _ := ctx.Value(UsernameContextKey).(string)
	return username
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

func UserHasPermission(rs *RaftStore, username string, perm Permission, channelID string) bool {
	user, err := rs.GetUserByUsername(username)
	if err != nil {
		user2, err2 := rs.GetUser(username)
		if err2 != nil {
			return false
		}
		user = user2
	}

	for _, roleName := range user.Roles {
		role, err := rs.GetRole(roleName)
		if err != nil {
			continue
		}
		for _, p := range role.Permissions {
			if p == "*" {
				if channelID != "" {
					if hasChannelAccess(user.Channels, channelID) {
						return true
					}
					continue
				}
				return true
			}
			if p == string(perm) {
				if strings.HasPrefix(string(perm), "channel:") && channelID != "" {
					if hasChannelAccess(user.Channels, channelID) {
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

func UserChannels(rs *RaftStore, username string) ([]string, error) {
	user, err := rs.GetUserByUsername(username)
	if err != nil {
		user2, err2 := rs.GetUser(username)
		if err2 != nil {
			return nil, err
		}
		user = user2
	}

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

	return user.Channels, nil
}

func IsAdmin(rs *RaftStore, username string) bool {
	return UserHasPermission(rs, username, "*", "")
}

func hasChannelAccess(userChannels []string, channelID string) bool {
	for _, ch := range userChannels {
		if ch == "*" || ch == channelID {
			return true
		}
	}
	return false
}

func GetUserPermissions(rs *RaftStore, username string) []string {
	perms := make(map[string]bool)
	user, err := rs.GetUserByUsername(username)
	if err != nil {
		user2, err2 := rs.GetUser(username)
		if err2 != nil {
			return nil
		}
		user = user2
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

	result := make([]string, 0, len(perms))
	for p := range perms {
		result = append(result, p)
	}
	return result
}