package service

import (
	"context"
	"slices"
	"strings"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
)

// normalizeIssuer canonicalizes an OIDC issuer URL (the "iss" claim) so the
// binding is stable regardless of a trailing slash. The subject is the OIDC
// "sub" claim; email and other mutable claims are never used.
func normalizeIssuer(issuer string) string {
	return strings.TrimSuffix(issuer, "/")
}

// ResolvePrincipal resolves a signed session token to the stored user it
// authenticates. Internal sessions resolve by user ID (falling back to the
// username for pre-migration cookies); OIDC sessions resolve strictly by the
// (provider, issuer, subject) external identity. It returns nil when no
// account is linked.
func (s *Service) ResolvePrincipal(ctx context.Context, tok *SessionToken) *domain.User {
	if tok == nil {
		return nil
	}
	if tok.Method == "oidc" {
		user, err := s.repo.GetUserByExternalIdentity(ctx, tok.Provider, normalizeIssuer(tok.Issuer), tok.Subject)
		if err != nil {
			return nil
		}
		return user
	}
	if tok.UserID != "" {
		if user, err := s.repo.GetUser(ctx, tok.UserID); err == nil {
			return user
		}
	}
	if tok.Username == "" {
		return nil
	}
	user, err := s.repo.GetUserByUsername(ctx, tok.Username)
	if err != nil {
		return nil
	}
	return user
}

// ResolvePrincipalByUsername resolves a user for the display-only username
// context (legacy callers and tests). It is never used to bypass the OIDC
// identity binding: OIDC sessions resolve through ResolvePrincipal.
func (s *Service) ResolvePrincipalByUsername(ctx context.Context, username string) *domain.User {
	if username == "" {
		return nil
	}
	user, err := s.repo.GetUserByUsername(ctx, username)
	if err != nil {
		user, err = s.repo.GetUser(ctx, username)
		if err != nil {
			return nil
		}
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

// mappingMatchesUser reports whether a role mapping applies to the principal.
// User mappings match the user ID or username; group mappings match only the
// verified OIDC groups carried by the session.
func mappingMatchesUser(m domain.RoleMapping, user *domain.User, groups []string) bool {
	switch m.Type {
	case "user":
		return m.Subject == user.ID || m.Subject == user.Username
	case "group":
		return slices.Contains(groups, m.Subject)
	default:
		return false
	}
}

func channelACLMatchesUser(a domain.ChannelRoleMapping, user *domain.User, groups []string) bool {
	switch a.Type {
	case "user":
		return a.Subject == user.ID || a.Subject == user.Username
	case "group":
		return slices.Contains(groups, a.Subject)
	default:
		return false
	}
}

func (s *Service) checkRoleMappingPermissions(ctx context.Context, user *domain.User, perm domain.Permission, channelID string, groups []string) bool {
	mappings, err := s.repo.ListRoleMappings(ctx)
	if err != nil {
		return false
	}
	for _, m := range mappings {
		if !mappingMatchesUser(m, user, groups) {
			continue
		}
		role, err := s.repo.GetRole(ctx, m.Role)
		if err != nil {
			continue
		}
		for _, p := range role.Permissions {
			if p != string(perm) && p != "*" {
				continue
			}
			// Collection access (no channel ID) requires a wildcard scope;
			// object access requires a wildcard or the exact channel. The
			// former broad `channelID == ""` match is intentionally removed:
			// it let a channel-scoped mapping grant every object/channel
			// permission.
			if channelID == "" {
				if m.ChannelScope == "*" {
					return true
				}
				continue
			}
			if m.ChannelScope == "*" || m.ChannelScope == channelID {
				return true
			}
		}
	}
	return false
}

func (s *Service) checkChannelRoleMappingPermissions(ctx context.Context, user *domain.User, perm domain.Permission, channelID string, groups []string) bool {
	acls, err := s.repo.ListChannelRoleMappings(ctx, channelID)
	if err != nil {
		return false
	}
	requiredLevel := 1
	if perm == domain.PermChannelWrite {
		requiredLevel = 2
	}
	for _, a := range acls {
		if !channelACLMatchesUser(a, user, groups) {
			continue
		}
		if roleLevel(a.Role) >= requiredLevel {
			return true
		}
	}
	return false
}

func roleLevel(role string) int {
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

func (s *Service) isChannelCreator(ctx context.Context, user *domain.User, channelID string) bool {
	ch, err := s.repo.GetChannel(ctx, channelID)
	if err != nil {
		return false
	}
	return ch.CreatedBy == user.ID || ch.CreatedBy == user.Username
}

// UserHasPermission evaluates whether the resolved principal holds perm,
// optionally scoped to a channel. groups are the verified OIDC groups from the
// signed session; they are never read from the user record.
func (s *Service) UserHasPermission(ctx context.Context, user *domain.User, groups []string, perm domain.Permission, channelID string) bool {
	if user == nil {
		return false
	}

	if s.checkGlobalRolePermissions(ctx, user, perm, channelID) {
		return true
	}

	if s.checkRoleMappingPermissions(ctx, user, perm, channelID, groups) {
		return true
	}

	if channelID != "" && strings.HasPrefix(string(perm), "channel:") {
		if perm == domain.PermChannelWrite || perm == domain.PermChannelRead {
			if s.checkChannelRoleMappingPermissions(ctx, user, perm, channelID, groups) {
				return true
			}
		}

		if s.isChannelCreator(ctx, user, channelID) {
			return true
		}
	}

	return false
}

// UserChannels returns the channels the principal may access. groups are the
// verified session groups.
func (s *Service) UserChannels(ctx context.Context, user *domain.User, groups []string) ([]string, error) {
	if user == nil {
		return []string{}, nil
	}

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
			if !mappingMatchesUser(m, user, groups) {
				continue
			}
			role, err := s.repo.GetRole(ctx, m.Role)
			if err != nil {
				continue
			}
			for _, p := range role.Permissions {
				if p == "*" {
					return allChannels()
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
			if s.isChannelCreator(ctx, user, ch.ID) {
				channelSet[ch.ID] = true
			}
			acls, err := s.repo.ListChannelRoleMappings(ctx, ch.ID)
			if err != nil {
				continue
			}
			for _, a := range acls {
				if channelACLMatchesUser(a, user, groups) {
					channelSet[ch.ID] = true
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

// IsAdmin reports whether the principal holds the wildcard permission.
func (s *Service) IsAdmin(ctx context.Context, user *domain.User, groups []string) bool {
	return s.UserHasPermission(ctx, user, groups, "*", "")
}

// HasChannelAccess reports whether the user's channel list grants access to
// the given channel (or wildcard).
func HasChannelAccess(userChannels []string, channelID string) bool {
	return hasChannelAccessList(userChannels, channelID)
}

// EffectivePermissions returns the flattened permission set granted to the
// principal by its roles and role mappings.
func (s *Service) EffectivePermissions(ctx context.Context, user *domain.User, groups []string) map[string]bool {
	perms := make(map[string]bool)
	if user == nil {
		return perms
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
		for _, m := range mappings {
			if !mappingMatchesUser(m, user, groups) {
				continue
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

	return perms
}

// GetUserPermissions returns the sorted-unique permission strings for a
// principal (used by /me).
func (s *Service) GetUserPermissions(ctx context.Context, user *domain.User, groups []string) []string {
	perms := s.EffectivePermissions(ctx, user, groups)
	result := make([]string, 0, len(perms))
	for p := range perms {
		result = append(result, p)
	}
	return result
}

// canAssignRole reports whether the actor may grant roleName to a target. A
// role granting the wildcard permission requires the actor to hold it; any
// other role requires the actor to hold every permission it grants.
func (s *Service) canAssignRole(ctx context.Context, actor *domain.User, actorGroups []string, roleName string) bool {
	role, err := s.repo.GetRole(ctx, roleName)
	if err != nil {
		return false
	}
	actorPerms := s.EffectivePermissions(ctx, actor, actorGroups)
	for _, p := range role.Permissions {
		if p == "*" {
			if !actorPerms["*"] {
				return false
			}
			continue
		}
		if !actorPerms["*"] && !actorPerms[p] {
			return false
		}
	}
	return true
}

// CanAssignRoles reports whether the actor may grant every role in roles, and
// that the actor's channel scope covers the requested channel scope.
func (s *Service) CanAssignRoles(ctx context.Context, actor *domain.User, actorGroups []string, roles []string, channelScope string) bool {
	if actor == nil {
		return false
	}
	for _, roleName := range roles {
		if !s.canAssignRole(ctx, actor, actorGroups, roleName) {
			return false
		}
	}
	if channelScope != "" && channelScope != "*" {
		if !hasChannelAccessList(actor.Channels, channelScope) {
			return false
		}
	}
	return true
}

// HasChannelRole reports whether the principal holds at least the given
// channel role (owner > write > read), via creator status or a channel ACL.
func (s *Service) HasChannelRole(ctx context.Context, user *domain.User, groups []string, channelID, role string) bool {
	if user == nil {
		return false
	}
	requiredLevel := roleLevel(role)

	if s.isChannelCreator(ctx, user, channelID) && requiredLevel <= 3 {
		return true
	}

	acls, err := s.repo.ListChannelRoleMappings(ctx, channelID)
	if err != nil {
		return false
	}
	for _, a := range acls {
		if !channelACLMatchesUser(a, user, groups) {
			continue
		}
		if roleLevel(a.Role) >= requiredLevel {
			return true
		}
	}
	return false
}
