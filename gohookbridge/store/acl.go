package store

import (
	"context"
	"net/http"
	"strings"
)

const UsernameContextKey = "username"

type contextKey string

const contextKeyProjectID contextKey = "project_id"

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

			projectID := ""
			if pid, ok := r.Context().Value(contextKeyProjectID).(string); ok {
				projectID = pid
			}

			if !UserHasPermission(rs, username, perm, projectID) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func UserHasPermission(rs *RaftStore, username string, perm Permission, projectID string) bool {
	user, err := rs.GetUserByUsername(username)
	if err != nil {
		// Try direct lookup by ID
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
				// Admin - check project scope if projectID provided
				if projectID != "" {
					if hasProjectAccess(user.Projects, projectID) {
						return true
					}
					continue
				}
				return true
			}
			if p == string(perm) {
				// Check project scope for project-scoped permissions
				if strings.HasPrefix(string(perm), "project:") && projectID != "" {
					if hasProjectAccess(user.Projects, projectID) {
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

func UserProjects(rs *RaftStore, username string) ([]string, error) {
	user, err := rs.GetUserByUsername(username)
	if err != nil {
		user2, err2 := rs.GetUser(username)
		if err2 != nil {
			return nil, err
		}
		user = user2
	}

	// Check if user has wildcard projects
	for _, roleName := range user.Roles {
		role, err := rs.GetRole(roleName)
		if err != nil {
			continue
		}
		for _, p := range role.Permissions {
			if p == "*" && hasProjectAccess(user.Projects, "*") {
				// Admin with wildcard - return all projects
				projects, _ := rs.ListProjects()
				ids := make([]string, len(projects))
				for i, pr := range projects {
					ids[i] = pr.ID
				}
				return ids, nil
			}
		}
	}

	return user.Projects, nil
}

func IsAdmin(rs *RaftStore, username string) bool {
	return UserHasPermission(rs, username, "*", "")
}

func hasProjectAccess(userProjects []string, projectID string) bool {
	for _, p := range userProjects {
		if p == "*" || p == projectID {
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