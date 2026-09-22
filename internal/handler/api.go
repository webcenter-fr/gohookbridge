package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"golang.org/x/crypto/bcrypt"
)

type apiHandler struct {
	svc *service.Service
}

// GlobalConfigResponse is the API DTO for the global configuration; the
// session secret is never returned to clients.
type GlobalConfigResponse struct {
	Server   ServerConfigResponse        `json:"server"`
	Defaults domain.DefaultChannelConfig `json:"defaults"`
}

type ServerConfigResponse struct {
	MaxBodySize        int    `json:"max_body_size"`
	BehindReverseProxy bool   `json:"behind_reverse_proxy"`
	CORSOrigin         string `json:"cors_origin"`
	Footer             string `json:"footer"`
	SessionSecret      string `json:"session_secret"`

	RateLimitEnabled       bool `json:"rate_limit_enabled"`
	RateLimitRequests      int  `json:"rate_limit_requests"`
	RateLimitWindowSeconds int  `json:"rate_limit_window_seconds"`

	BanEnabled           bool `json:"ban_enabled"`
	BanMaxUniqueFailures int  `json:"ban_max_unique_failures"`
	BanWindowSeconds     int  `json:"ban_window_seconds"`
	BanDurationSeconds   int  `json:"ban_duration_seconds"`
}

func RegisterAPIHandlers(r chi.Router, svc *service.Service) {
	h := &apiHandler{svc: svc}

	r.Route("/channels", func(r chi.Router) {
		r.Use(RequirePermission(svc, domain.PermChannelRead))
		r.Get("/", h.listChannels)
		r.Post("/", h.createChannel)
		r.Route("/{id}", func(r chi.Router) {
			r.Use(h.channelCtx)
			r.Get("/", h.getChannel)
			r.Put("/", h.updateChannel)
			r.Delete("/", h.deleteChannel)
			r.Post("/generate-secret", h.generateSecret)
			r.With(RequirePermission(svc, domain.PermChannelWrite)).Post("/access-tokens", h.createAccessToken)
			r.Get("/access-tokens", h.listAccessTokens)
			r.With(RequirePermission(svc, domain.PermChannelWrite)).Delete("/access-tokens/{tokenID}", h.deleteAccessToken)
			r.With(RequirePermission(svc, domain.PermChannelWrite)).Put("/access-mode", h.updateAccessMode)
			r.Get("/acl", h.listChannelACL)
			r.With(RequireChannelACLPermission(svc)).Post("/acl", h.addChannelACLEntry)
			r.With(RequireChannelACLPermission(svc)).Delete("/acl/{entryID}", h.deleteChannelACLEntry)
		})
	})

	r.Route("/global", func(r chi.Router) {
		r.Use(RequirePermission(svc, domain.PermGlobalRead))
		r.Get("/", h.getGlobalConfig)
		r.Put("/", h.updateGlobalConfig)
	})

	r.Route("/users", func(r chi.Router) {
		r.Use(RequirePermission(svc, domain.PermUsersRead))
		r.Get("/", h.listUsers)
		r.Post("/", h.createUser)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.getUser)
			r.Put("/", h.updateUser)
			r.Delete("/", h.deleteUser)
		})
	})

	r.Route("/rbac", func(r chi.Router) {
		r.Use(RequirePermission(svc, domain.PermRBACRead))
		r.Get("/roles", h.listRoles)
		r.Get("/bindings", h.listBindings)
		r.Put("/bindings/{userID}", h.updateBinding)
		r.Get("/mappings", h.listRoleMappings)
		r.With(RequirePermission(svc, domain.PermRBACWrite)).Post("/mappings", h.createRoleMapping)
		r.With(RequirePermission(svc, domain.PermRBACWrite)).Delete("/mappings/{id}", h.deleteRoleMapping)
	})

	r.Route("/oidc", func(r chi.Router) {
		r.Use(RequirePermission(svc, domain.PermGlobalWrite))
		r.Get("/providers", h.listOIDCProviders)
		r.Put("/providers", h.updateAllOIDCProviders)
		r.Put("/providers/{id}", h.updateOIDCProvider)
		r.Delete("/providers/{id}", h.deleteOIDCProvider)
	})

	r.Get("/me", h.getMe)
}

func (h *apiHandler) channelCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		ctx := context.WithValue(r.Context(), contextKeyChannelID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *apiHandler) listChannels(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	ctx := r.Context()

	username := GetUsernameFromContext(ctx)
	allowedChannels, _ := h.svc.UserChannels(ctx, username)

	channels, err := h.svc.ListChannels(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	filtered := make([]*domain.Channel, 0)
	for _, p := range channels {
		if service.HasChannelAccess(allowedChannels, p.ID) {
			sanitizeChannelAPI(p)
			filtered = append(filtered, p)
		}
	}

	if format == "csv" {
		writeCSV(w, filtered)
		return
	}

	writeJSON(w, http.StatusOK, filtered)
}

func (h *apiHandler) createChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var ch domain.Channel
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	username := GetUsernameFromContext(ctx)
	ch.CreatedBy = username
	if msg := validateStruct(&ch); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := h.svc.CreateChannel(ctx, &ch); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = h.svc.CreateChannelRoleMapping(ctx, &domain.ChannelRoleMapping{
		ChannelID: ch.ID,
		Type:      "user",
		Subject:   username,
		Role:      "owner",
	})
	writeJSON(w, http.StatusCreated, &ch)
}

func (h *apiHandler) getChannel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := h.svc.GetChannel(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	domain.MigrateChannel(p)
	sanitizeChannelAPI(p)
	writeJSON(w, http.StatusOK, p)
}

func (h *apiHandler) updateChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var ch domain.Channel
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ch.ID = id
	if msg := validateStruct(&ch); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := h.svc.UpdateChannel(ctx, &ch); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, &ch)
}

func (h *apiHandler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.DeleteChannel(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *apiHandler) getGlobalConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.svc.GetGlobalConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := GlobalConfigResponse{
		Server: ServerConfigResponse{
			MaxBodySize:        cfg.Server.MaxBodySize,
			BehindReverseProxy: cfg.Server.BehindReverseProxy,
			CORSOrigin:         cfg.Server.CORSOrigin,
			Footer:             cfg.Server.Footer,
			SessionSecret:      "<redacted>",

			RateLimitEnabled:       cfg.Server.RateLimitEnabled,
			RateLimitRequests:      cfg.Server.RateLimitRequests,
			RateLimitWindowSeconds: cfg.Server.RateLimitWindowSeconds,

			BanEnabled:           cfg.Server.BanEnabled,
			BanMaxUniqueFailures: cfg.Server.BanMaxUniqueFailures,
			BanWindowSeconds:     cfg.Server.BanWindowSeconds,
			BanDurationSeconds:   cfg.Server.BanDurationSeconds,
		},
		Defaults: cfg.Defaults,
	}
	if cfg.Server.SessionSecret == "" {
		resp.Server.SessionSecret = ""
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *apiHandler) updateGlobalConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var cfg domain.GlobalConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if cfg.Server.SessionSecret == "" {
		existing, err := h.svc.GetGlobalConfig(ctx)
		if err == nil {
			cfg.Server.SessionSecret = existing.Server.SessionSecret
		}
	}
	if err := h.svc.UpdateGlobalConfig(ctx, &cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, &cfg)
}

func (h *apiHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.svc.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	masked := make([]map[string]any, len(users))
	for i, u := range users {
		masked[i] = map[string]any{
			"id":       u.ID,
			"username": u.Username,
			"roles":    u.Roles,
			"channels": u.Channels,
		}
	}
	writeJSON(w, http.StatusOK, masked)
}

func (h *apiHandler) createUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var input struct {
		Username string   `json:"username" validate:"required,min=1,max=128"`
		Password string   `json:"password"`
		Roles    []string `json:"roles"`
		Channels []string `json:"channels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if msg := validateStruct(&input); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	user := &domain.User{
		ID:           input.Username,
		Username:     input.Username,
		PasswordHash: string(hash),
		Roles:        input.Roles,
		Channels:     input.Channels,
	}
	if err := h.svc.CreateUser(ctx, user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": user.ID, "username": user.Username})
}

func (h *apiHandler) getUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	u, err := h.svc.GetUser(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       u.ID,
		"username": u.Username,
		"roles":    u.Roles,
		"channels": u.Channels,
	})
}

func hasAdminRole(roles []string) bool {
	for _, r := range roles {
		if r == "admin" {
			return true
		}
	}
	return false
}

func (h *apiHandler) updateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var input struct {
		Username string   `json:"username"           validate:"max=128"`
		Password string   `json:"password,omitempty"`
		Roles    []string `json:"roles"`
		Channels []string `json:"channels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if msg := validateStruct(&input); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	u, err := h.svc.GetUser(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if input.Roles != nil && hasAdminRole(u.Roles) && !hasAdminRole(input.Roles) {
		writeError(w, http.StatusBadRequest, "cannot remove admin role")
		return
	}
	if input.Username != "" {
		u.Username = input.Username
	}
	if input.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		u.PasswordHash = string(hash)
	}
	if input.Roles != nil {
		u.Roles = input.Roles
	}
	if input.Channels != nil {
		u.Channels = input.Channels
	}
	if err := h.svc.UpdateUser(ctx, u); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": u.ID, "username": u.Username})
}

func (h *apiHandler) deleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	u, err := h.svc.GetUser(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if hasAdminRole(u.Roles) {
		users, err := h.svc.ListUsers(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return
		}
		adminCount := 0
		for _, usr := range users {
			if hasAdminRole(usr.Roles) && usr.ID != id {
				adminCount++
			}
		}
		if adminCount == 0 {
			writeError(w, http.StatusBadRequest, "cannot delete the last admin user")
			return
		}
	}
	if err := h.svc.DeleteUser(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *apiHandler) listRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.svc.ListRoles(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, roles)
}

func (h *apiHandler) listBindings(w http.ResponseWriter, r *http.Request) {
	bindings, err := h.svc.ListBindings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, bindings)
}

func (h *apiHandler) updateBinding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "userID")
	var binding domain.UserBinding
	if err := json.NewDecoder(r.Body).Decode(&binding); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	binding.UserID = userID
	if msg := validateStruct(&binding); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	u, err := h.svc.GetUser(ctx, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get user")
		return
	}
	if hasAdminRole(u.Roles) && !hasAdminRole(binding.Roles) {
		writeError(w, http.StatusBadRequest, "cannot remove admin role")
		return
	}
	if err := h.svc.UpdateUserBinding(ctx, &binding); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, &binding)
}

func (h *apiHandler) getMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	username := GetUsernameFromContext(ctx)

	if h.svc.IsSetupMode(ctx) {
		writeJSON(w, http.StatusOK, map[string]bool{"setup_mode": true})
		return
	}

	if username == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	user, err := h.svc.GetUserByUsername(ctx, username)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	permissions := h.svc.GetUserPermissions(ctx, username)
	channels, _ := h.svc.UserChannels(ctx, username)

	providers, _ := h.svc.OIDCProviders(ctx)
	oidcList := make([]map[string]string, 0, len(providers))
	for _, p := range providers {
		oidcList = append(oidcList, map[string]string{"id": p.ID, "name": p.Name})
	}
	users, _ := h.svc.ListUsers(ctx)

	writeJSON(w, http.StatusOK, map[string]any{
		"username":    user.Username,
		"roles":       user.Roles,
		"channels":    channels,
		"permissions": permissions,
		"auth_methods": map[string]any{
			"oidc_providers": oidcList,
			"local_enabled":  len(users) > 0,
		},
	})
}

func (h *apiHandler) generateSecret(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	ch, err := h.svc.GetChannel(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate secret")
		return
	}
	secret := hex.EncodeToString(b)

	ch.WebhookSecret = secret
	if err := h.svc.UpdateChannel(ctx, ch); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"webhook_secret": secret})
}

func (h *apiHandler) createAccessToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var body struct {
		Name  string `json:"name"`
		Scope string `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Name == "" {
		body.Name = "default"
	}
	if body.Scope == "" {
		body.Scope = "both"
	}
	if body.Scope != "produce" && body.Scope != "consume" && body.Scope != "both" {
		writeError(w, http.StatusBadRequest, "scope must be 'produce', 'consume', or 'both'")
		return
	}
	raw, token, err := h.svc.CreateAccessToken(ctx, id, body.Name, body.Scope)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      raw,
		"id":         token.ID,
		"name":       token.Name,
		"scope":      token.Scope,
		"created_at": token.CreatedAt,
	})
}

func (h *apiHandler) listAccessTokens(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	ch, err := h.svc.GetChannel(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	tokens := make([]map[string]string, 0, len(ch.AccessTokens))
	for _, t := range ch.AccessTokens {
		tokens = append(tokens, map[string]string{
			"id":         t.ID,
			"name":       t.Name,
			"scope":      t.Scope,
			"created_at": t.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_mode": ch.AccessMode,
		"tokens":      tokens,
	})
}

func (h *apiHandler) deleteAccessToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	tokenID := chi.URLParam(r, "tokenID")
	if err := h.svc.DeleteAccessToken(ctx, id, tokenID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *apiHandler) updateAccessMode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var body struct {
		AccessMode string `json:"access_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.AccessMode != "public" && body.AccessMode != "token" {
		writeError(w, http.StatusBadRequest, "access_mode must be 'public' or 'token'")
		return
	}
	ch, err := h.svc.GetChannel(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if body.AccessMode == "token" && len(ch.AccessTokens) == 0 {
		writeError(w, http.StatusBadRequest, "cannot enable token access without tokens")
		return
	}
	ch.AccessMode = body.AccessMode
	if err := h.svc.UpdateChannel(ctx, ch); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"access_mode": ch.AccessMode})
}

func (h *apiHandler) listRoleMappings(w http.ResponseWriter, r *http.Request) {
	mappings, err := h.svc.ListRoleMappings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mappings)
}

func (h *apiHandler) createRoleMapping(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var m domain.RoleMapping
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if m.Type != "user" && m.Type != "group" {
		writeError(w, http.StatusBadRequest, "type must be 'user' or 'group'")
		return
	}
	if m.Subject == "" {
		writeError(w, http.StatusBadRequest, "subject required")
		return
	}
	if m.Role == "" {
		writeError(w, http.StatusBadRequest, "role required")
		return
	}
	if m.ChannelScope == "" {
		m.ChannelScope = "*"
	}
	if err := h.svc.CreateRoleMapping(ctx, &m); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, &m)
}

func (h *apiHandler) deleteRoleMapping(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.DeleteRoleMapping(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *apiHandler) listChannelACL(w http.ResponseWriter, r *http.Request) {
	channelID := chi.URLParam(r, "id")
	acls, err := h.svc.ListChannelRoleMappings(r.Context(), channelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, acls)
}

func (h *apiHandler) addChannelACLEntry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	channelID := chi.URLParam(r, "id")
	var m domain.ChannelRoleMapping
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if m.Type != "user" && m.Type != "group" {
		writeError(w, http.StatusBadRequest, "type must be 'user' or 'group'")
		return
	}
	if m.Subject == "" {
		writeError(w, http.StatusBadRequest, "subject required")
		return
	}
	if m.Role != "owner" && m.Role != "write" && m.Role != "read" {
		writeError(w, http.StatusBadRequest, "role must be 'owner', 'write', or 'read'")
		return
	}
	m.ChannelID = channelID
	if err := h.svc.CreateChannelRoleMapping(ctx, &m); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, &m)
}

func (h *apiHandler) deleteChannelACLEntry(w http.ResponseWriter, r *http.Request) {
	channelID := chi.URLParam(r, "id")
	entryID := chi.URLParam(r, "entryID")
	if err := h.svc.DeleteChannelRoleMapping(r.Context(), channelID, entryID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *apiHandler) listOIDCProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.svc.OIDCProviders(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if providers == nil {
		providers = []domain.OIDCProvider{}
	}
	// Redact client_secret in list responses — only the write endpoints handle full secrets
	for i := range providers {
		if providers[i].ClientSecret != "" {
			providers[i].ClientSecret = "<redacted>"
		}
	}
	writeJSON(w, http.StatusOK, providers)
}

func (h *apiHandler) updateAllOIDCProviders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var providers []domain.OIDCProvider
	if err := json.NewDecoder(r.Body).Decode(&providers); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if providers == nil {
		providers = []domain.OIDCProvider{}
	}
	for i := range providers {
		if providers[i].GroupsClaim == "" {
			providers[i].GroupsClaim = "groups"
		}
	}
	if err := h.svc.SetOIDCProviders(ctx, providers); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, providers)
}

func (h *apiHandler) updateOIDCProvider(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var updated domain.OIDCProvider
	if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	providers, err := h.svc.OIDCProviders(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	found := false
	for i, p := range providers {
		if p.ID == id {
			if updated.GroupsClaim == "" {
				updated.GroupsClaim = "groups"
			}
			providers[i] = updated
			found = true
			break
		}
	}
	if !found {
		if updated.GroupsClaim == "" {
			updated.GroupsClaim = "groups"
		}
		providers = append(providers, updated)
	}
	if err := h.svc.SetOIDCProviders(ctx, providers); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *apiHandler) deleteOIDCProvider(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	providers, err := h.svc.OIDCProviders(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var updated []domain.OIDCProvider
	for _, p := range providers {
		if p.ID != id {
			updated = append(updated, p)
		}
	}
	if err := h.svc.SetOIDCProviders(ctx, updated); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func sanitizeChannelAPI(p *domain.Channel) {
	p.EncryptionPrivateKey = ""
	if len(p.AccessTokens) == 0 {
		p.AccessTokens = nil
		return
	}
	sanitized := make([]domain.ChannelAccessToken, len(p.AccessTokens))
	for i, t := range p.AccessTokens {
		t.TokenHash = ""
		sanitized[i] = t
	}
	p.AccessTokens = sanitized
}

func writeCSV(w http.ResponseWriter, channels []*domain.Channel) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=channels.csv")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("id\n"))
	for _, ch := range channels {
		fmt.Fprintf(w, "%s\n", ch.ID)
	}
}

func validateStruct(s interface{}) string {
	if err := domain.ValidateStruct(s); err != nil {
		var ve validator.ValidationErrors
		if !errors.As(err, &ve) {
			return err.Error()
		}
		var msgs []string
		for _, e := range ve {
			msgs = append(msgs, fmt.Sprintf("%s: %s", e.Field(), e.Tag()))
		}
		return strings.Join(msgs, "; ")
	}
	return ""
}

func SetupModeMiddleware(svc *service.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if svc.IsSetupMode(r.Context()) {
				setupEnd := svc.GetSetupModeEndTime(r.Context())
				if setupEnd.IsZero() {
					setupEnd = time.Now().Add(5 * time.Minute)
					if err := svc.SetSetupModeEndTime(r.Context(), setupEnd); err != nil {
						writeError(w, http.StatusInternalServerError, "failed to set setup mode end time")
						return
					}
				}
				if time.Now().Before(setupEnd) {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "Setup mode expired", http.StatusUnauthorized)
		})
	}
}

// AdaptiveAuthMiddleware combines setup mode and auth checks.
// If setup mode is active, requests pass through.
// Otherwise, requests proceed to inner middleware (RequirePermission) which handles auth.
func AdaptiveAuthMiddleware(svc *service.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if svc.IsSetupMode(r.Context()) {
				setupEnd := svc.GetSetupModeEndTime(r.Context())
				if setupEnd.IsZero() {
					setupEnd = time.Now().Add(5 * time.Minute)
					if err := svc.SetSetupModeEndTime(r.Context(), setupEnd); err != nil {
						writeError(w, http.StatusInternalServerError, "failed to set setup mode end time")
						return
					}
				}
				if time.Now().Before(setupEnd) {
					next.ServeHTTP(w, r)
					return
				}
			}
			// Setup mode expired or never active — pass through to RequirePermission middleware
			next.ServeHTTP(w, r)
		})
	}
}
