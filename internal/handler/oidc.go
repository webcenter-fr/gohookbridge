package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/service"
)

const oidcStateCookieName = "oidc_state"

type OIDCDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
}

type OIDCHandler struct {
	Provider      domain.OIDCProvider
	Discovery     *OIDCDiscovery
	SessionSecret [32]byte
	PublicURL     string
	SecureCookies bool
}

func NewOIDCHandler(provider domain.OIDCProvider, sessionSecret [32]byte, publicURL string, secureCookies bool) (*OIDCHandler, error) {
	if provider.GroupsClaim == "" {
		provider.GroupsClaim = "groups"
	}
	discURL := strings.TrimSuffix(provider.IssuerURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, discURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var disc OIDCDiscovery
	if err := json.Unmarshal(body, &disc); err != nil {
		return nil, err
	}
	return &OIDCHandler{
		Provider:      provider,
		Discovery:     &disc,
		SessionSecret: sessionSecret,
		PublicURL:     publicURL,
		SecureCookies: secureCookies,
	}, nil
}

// safeRedirectPath validates a post-login redirect target. Only same-site
// relative paths are allowed; absolute URLs, protocol-relative URLs ("//") and
// backslash tricks are rejected to prevent open redirects (CWE-601).
func safeRedirectPath(redirect string) string {
	if redirect == "" {
		return "/"
	}
	if strings.HasPrefix(redirect, "/") &&
		!strings.HasPrefix(redirect, "//") &&
		!strings.HasPrefix(redirect, "/\\") &&
		!strings.Contains(redirect, "\r") &&
		!strings.Contains(redirect, "\n") {
		return redirect
	}
	return "/"
}

func (h *OIDCHandler) LoginHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		redirect := safeRedirectPath(r.URL.Query().Get("redirect"))

		state, err := service.GenerateRandomHex()
		if err != nil {
			http.Error(w, "Failed to generate state: "+err.Error(), http.StatusInternalServerError)
			return
		}
		nonce, err := service.GenerateRandomHex()
		if err != nil {
			http.Error(w, "Failed to generate nonce: "+err.Error(), http.StatusInternalServerError)
			return
		}

		stateValue := fmt.Sprintf("%s|%s", state, redirect)
		http.SetCookie(w, &http.Cookie{
			Name:     oidcStateCookieName,
			Value:    stateValue,
			Path:     "/",
			HttpOnly: true,
			Secure:   h.SecureCookies,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   300,
		})

		scopes := strings.Join(h.Provider.Scopes, " ")
		redirectURI := h.PublicURL + "/auth/oidc/" + h.Provider.ID + "/callback"
		authURL := fmt.Sprintf("%s?response_type=code&client_id=%s&scope=%s&state=%s&nonce=%s&redirect_uri=%s",
			h.Discovery.AuthorizationEndpoint,
			url.QueryEscape(h.Provider.ClientID),
			url.QueryEscape(scopes),
			url.QueryEscape(state),
			url.QueryEscape(nonce),
			url.QueryEscape(redirectURI),
		)
		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

func (h *OIDCHandler) CallbackHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")
		if code == "" || state == "" {
			http.Error(w, "Missing code or state", http.StatusBadRequest)
			return
		}

		cookie, err := r.Cookie(oidcStateCookieName)
		if err != nil {
			http.Error(w, "Missing state cookie", http.StatusBadRequest)
			return
		}
		parts := strings.SplitN(cookie.Value, "|", 2)
		if len(parts) != 2 || parts[0] != state {
			http.Error(w, "Invalid state", http.StatusBadRequest)
			return
		}
		redirect := safeRedirectPath(parts[1])

		clearOIDCStateCookie(w, h.SecureCookies)

		token, err := h.exchangeCode(code, r)
		if err != nil {
			http.Error(w, "Token exchange failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		accessToken, ok := token["access_token"].(string)
		if !ok {
			http.Error(w, "No access token in response", http.StatusInternalServerError)
			return
		}

		userInfo, err := h.getUserInfo(accessToken)
		if err != nil {
			http.Error(w, "Userinfo failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		sub, _ := userInfo["sub"].(string)
		email, _ := userInfo["email"].(string)
		username := email
		if username == "" {
			username = sub
		}

		groups := extractGroupsFromToken(userInfo, h.Provider.GroupsClaim)

		sessionTok := &service.SessionToken{
			Username:  username,
			Method:    "oidc",
			Provider:  h.Provider.ID,
			ExpiresAt: time.Now().Unix() + sessionMaxAge,
			Groups:    groups,
		}
		encoded, err := service.EncodeSession(sessionTok, h.SessionSecret)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		setSessionCookie(w, encoded, h.SecureCookies)
		http.Redirect(w, r, redirect, http.StatusFound)
	}
}

func (h *OIDCHandler) exchangeCode(code string, r *http.Request) (map[string]any, error) {
	redirectURI := h.PublicURL + "/auth/oidc/" + h.Provider.ID + "/callback"
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {h.Provider.ClientID},
		"client_secret": {h.Provider.ClientSecret},
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.Discovery.TokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (h *OIDCHandler) getUserInfo(accessToken string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.Discovery.UserinfoEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func clearOIDCStateCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func extractGroupsFromToken(token map[string]any, claimName string) []string {
	if claimName == "" {
		claimName = "groups"
	}
	raw, ok := token[claimName]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []any:
		groups := make([]string, 0, len(v))
		for _, g := range v {
			if s, ok := g.(string); ok {
				groups = append(groups, s)
			}
		}
		return groups
	case []string:
		return v
	case string:
		return strings.Split(v, ",")
	default:
		return nil
	}
}
