package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"golang.org/x/crypto/bcrypt"
)

type apiHandler struct {
	rs *RaftStore
}

func RegisterAPIHandlers(r chi.Router, rs *RaftStore) {
	h := &apiHandler{rs: rs}

	r.Route("/channels", func(r chi.Router) {
		r.Use(RequirePermission(rs, PermChannelRead))
		r.Get("/", h.listChannels)
		r.Post("/", h.createChannel)
		r.Route("/{id}", func(r chi.Router) {
			r.Use(h.channelCtx)
			r.Get("/", h.getChannel)
			r.Put("/", h.updateChannel)
			r.Delete("/", h.deleteChannel)
			r.Post("/generate-secret", h.generateSecret)
		})
	})

	r.Route("/global", func(r chi.Router) {
		r.Use(RequirePermission(rs, PermGlobalRead))
		r.Get("/", h.getGlobalConfig)
		r.Put("/", h.updateGlobalConfig)
	})

	r.Route("/users", func(r chi.Router) {
		r.Use(RequirePermission(rs, PermUsersRead))
		r.Get("/", h.listUsers)
		r.Post("/", h.createUser)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.getUser)
			r.Put("/", h.updateUser)
			r.Delete("/", h.deleteUser)
		})
	})

	r.Route("/rbac", func(r chi.Router) {
		r.Use(RequirePermission(rs, PermRBACRead))
		r.Get("/roles", h.listRoles)
		r.Get("/bindings", h.listBindings)
		r.Put("/bindings/{userID}", h.updateBinding)
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

	username := GetUsernameFromContext(r.Context())
	allowedChannels, _ := UserChannels(h.rs, username)

	channels, err := h.rs.ListChannels()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	filtered := make([]*Channel, 0)
	for _, p := range channels {
		if hasChannelAccess(allowedChannels, p.ID) {
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
	var ch Channel
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if msg := validateStruct(&ch); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := h.rs.CreateChannel(&ch); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, &ch)
}

func (h *apiHandler) getChannel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := h.rs.GetChannel(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	migrateChannel(p)
	writeJSON(w, http.StatusOK, p)
}

func (h *apiHandler) updateChannel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var ch Channel
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ch.ID = id
	if msg := validateStruct(&ch); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := h.rs.UpdateChannel(&ch); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, &ch)
}

func (h *apiHandler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.rs.DeleteChannel(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *apiHandler) getGlobalConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.rs.GetGlobalConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := GlobalConfigResponse{
		Server: ServerConfigResponse{
			MaxBodySize:   cfg.Server.MaxBodySize,
			TrustProxy:    cfg.Server.TrustProxy,
			CORSOrigin:    cfg.Server.CORSOrigin,
			Footer:        cfg.Server.Footer,
			SessionSecret: "<redacted>",
		},
		Defaults: cfg.Defaults,
	}
	if cfg.Server.SessionSecret == "" {
		resp.Server.SessionSecret = ""
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *apiHandler) updateGlobalConfig(w http.ResponseWriter, r *http.Request) {
	var cfg GlobalConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if cfg.Server.SessionSecret == "" {
		existing, err := h.rs.GetGlobalConfig()
		if err == nil {
			cfg.Server.SessionSecret = existing.Server.SessionSecret
		}
	}
	if err := h.rs.UpdateGlobalConfig(&cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, &cfg)
}

func (h *apiHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.rs.ListUsers()
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

	user := &User{
		ID:           input.Username,
		Username:     input.Username,
		PasswordHash: string(hash),
		Roles:        input.Roles,
		Channels:     input.Channels,
	}
	if err := h.rs.CreateUser(user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": user.ID, "username": user.Username})
}

func (h *apiHandler) getUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	u, err := h.rs.GetUser(id)
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

func (h *apiHandler) updateUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var input struct {
		Username string   `json:"username" validate:"max=128"`
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

	u, err := h.rs.GetUser(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
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
	if err := h.rs.UpdateUser(u); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": u.ID, "username": u.Username})
}

func (h *apiHandler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.rs.DeleteUser(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *apiHandler) listRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.rs.ListRoles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, roles)
}

func (h *apiHandler) listBindings(w http.ResponseWriter, r *http.Request) {
	bindings, err := h.rs.ListBindings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, bindings)
}

func (h *apiHandler) updateBinding(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	var binding UserBinding
	if err := json.NewDecoder(r.Body).Decode(&binding); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	binding.UserID = userID
	if msg := validateStruct(&binding); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := h.rs.UpdateUserBinding(&binding); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, &binding)
}

func (h *apiHandler) getMe(w http.ResponseWriter, r *http.Request) {
	username := GetUsernameFromContext(r.Context())

	if h.rs.IsSetupMode() {
		writeJSON(w, http.StatusOK, map[string]bool{"setup_mode": true})
		return
	}

	if username == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	user, err := h.rs.GetUserByUsername(username)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	permissions := GetUserPermissions(h.rs, username)
	channels, _ := UserChannels(h.rs, username)

	providers, _ := h.rs.OIDCProviders()
	oidcList := make([]map[string]string, 0, len(providers))
	for _, p := range providers {
		oidcList = append(oidcList, map[string]string{"id": p.ID, "name": p.Name})
	}
	users, _ := h.rs.ListUsers()

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
	id := chi.URLParam(r, "id")
	ch, err := h.rs.GetChannel(id)
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
	if err := h.rs.UpdateChannel(ch); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"webhook_secret": secret})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeCSV(w http.ResponseWriter, channels []*Channel) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=channels.csv")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("id,name\n"))
	for _, ch := range channels {
		fmt.Fprintf(w, "%s,%s\n", ch.ID, ch.Name)
	}
}

func validateStruct(s interface{}) string {
	if err := validate.Struct(s); err != nil {
		ve, ok := err.(validator.ValidationErrors)
		if !ok {
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

func (rs *RaftStore) SetupModeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rs.IsSetupMode() {
			setupEnd := rs.GetSetupModeEndTime()
			if setupEnd.IsZero() {
				setupEnd = time.Now().Add(5 * time.Minute)
				if err := rs.SetSetupModeEndTime(setupEnd); err != nil {
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

// AdaptiveAuthMiddleware combines setup mode and auth checks.
// If setup mode is active, requests pass through.
// Otherwise, requests proceed to inner middleware (RequirePermission) which handles auth.
func AdaptiveAuthMiddleware(rs *RaftStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rs.IsSetupMode() {
				setupEnd := rs.GetSetupModeEndTime()
				if setupEnd.IsZero() {
					setupEnd = time.Now().Add(5 * time.Minute)
					if err := rs.SetSetupModeEndTime(setupEnd); err != nil {
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