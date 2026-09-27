package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/service"
)

const (
	oidcStateCookieName = "oidc_state"
	oidcNonceCookieName = "oidc_nonce"
	oidcPKCECookieName  = "oidc_pkce"
)

// oidcDiscoveryTimeout bounds the provider discovery/JWKS fetches performed at
// startup so a hanging issuer cannot block the server from serving forever.
const oidcDiscoveryTimeout = 10 * time.Second

// oidcHTTPTimeout bounds every outbound OIDC HTTP call (discovery, token
// exchange, userinfo) so a hanging or malicious issuer cannot stall handlers.
const oidcHTTPTimeout = 10 * time.Second

// errMissingPKCEVerifier is returned by exchangeCode when the PKCE cookie is
// absent; the callback translates it to a 400 instead of a 500.
var errMissingPKCEVerifier = errors.New("missing pkce code_verifier")

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
	svc           *service.Service
	client        *http.Client
	verifier      *oidc.IDTokenVerifier
}

func NewOIDCHandler(provider domain.OIDCProvider, sessionSecret [32]byte, publicURL string, secureCookies bool, svc *service.Service) (*OIDCHandler, error) {
	if provider.GroupsClaim == "" {
		provider.GroupsClaim = "groups"
	}
	// Bound the startup fetches: a hanging issuer must not block boot. The
	// verifier's JWKS refresh uses its own background context (go-oidc), so
	// canceling this context after discovery is safe.
	discCtx, cancel := context.WithTimeout(context.Background(), oidcDiscoveryTimeout)
	defer cancel()

	client := &http.Client{Timeout: oidcHTTPTimeout}

	discURL := strings.TrimSuffix(provider.IssuerURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(discCtx, http.MethodGet, discURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc discovery returned status %d", resp.StatusCode)
	}
	var disc OIDCDiscovery
	if err := json.Unmarshal(body, &disc); err != nil {
		return nil, err
	}
	// go-oidc provider for ID-token verification (signature/JWKS, iss, aud,
	// exp). The manual OIDCDiscovery above is kept for token exchange and
	// userinfo.
	p, err := oidc.NewProvider(discCtx, provider.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc provider discovery: %w", err)
	}
	return &OIDCHandler{
		Provider:      provider,
		Discovery:     &disc,
		SessionSecret: sessionSecret,
		PublicURL:     publicURL,
		SecureCookies: secureCookies,
		svc:           svc,
		client:        client,
		verifier:      p.Verifier(&oidc.Config{ClientID: provider.ClientID}),
	}, nil
}

// resolveOIDCUsername maps an OIDC subject to an internal user's username
// (subject→user matching), falling back to email then sub for backward compat.
func resolveOIDCUsername(ctx context.Context, svc *service.Service, sub, email string) string {
	if sub != "" {
		if u, err := svc.GetUserByOIDCSubject(ctx, sub); err == nil {
			return u.Username
		}
	}
	if email != "" {
		return email
	}
	return sub
}

// generatePKCE returns a PKCE (RFC 7636) verifier and its S256 challenge.
func generatePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
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
		//nolint:gosec // Secure reflects the effective TLS deployment, derived once at startup
		http.SetCookie(w, &http.Cookie{
			Name:     oidcStateCookieName,
			Value:    stateValue,
			Path:     "/",
			HttpOnly: true,
			Secure:   h.SecureCookies,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   300,
		})
		//nolint:gosec // Secure reflects the effective TLS deployment, derived once at startup
		http.SetCookie(w, &http.Cookie{
			Name:     oidcNonceCookieName,
			Value:    nonce,
			Path:     "/",
			HttpOnly: true,
			Secure:   h.SecureCookies,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   300,
		})

		verifier, challenge, err := generatePKCE()
		if err != nil {
			http.Error(w, "Failed to generate PKCE verifier: "+err.Error(), http.StatusInternalServerError)
			return
		}
		//nolint:gosec // Secure reflects the effective TLS deployment, derived once at startup
		http.SetCookie(w, &http.Cookie{
			Name:     oidcPKCECookieName,
			Value:    verifier,
			Path:     "/",
			HttpOnly: true,
			Secure:   h.SecureCookies,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   300,
		})

		scopes := strings.Join(h.Provider.Scopes, " ")
		redirectURI := h.PublicURL + "/auth/oidc/" + h.Provider.ID + "/callback"
		authURL := fmt.Sprintf("%s?response_type=code&client_id=%s&scope=%s&state=%s&nonce=%s&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256",
			h.Discovery.AuthorizationEndpoint,
			url.QueryEscape(h.Provider.ClientID),
			url.QueryEscape(scopes),
			url.QueryEscape(state),
			url.QueryEscape(nonce),
			url.QueryEscape(redirectURI),
			url.QueryEscape(challenge),
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
			if errors.Is(err, errMissingPKCEVerifier) {
				http.Error(w, "Missing PKCE verifier", http.StatusBadRequest)
				return
			}
			http.Error(w, "Token exchange failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		clearOIDCPKCECookie(w, h.SecureCookies)
		accessToken, ok := token["access_token"].(string)
		if !ok {
			http.Error(w, "No access token in response", http.StatusInternalServerError)
			return
		}

		// Prefer the verified ID token when the provider returns one (CWE-345).
		rawIDToken, _ := token["id_token"].(string)
		if rawIDToken != "" {
			nonce := ""
			if c, cookieErr := r.Cookie(oidcNonceCookieName); cookieErr == nil {
				nonce = c.Value
			}
			idToken, claims, verifyErr := h.verifyIDToken(r.Context(), rawIDToken, nonce)
			if verifyErr != nil {
				// Log the internal detail; never leak validation internals to
				// the client.
				fmt.Fprintf(os.Stderr, "WARNING: oidc id token validation failed: %v\n", verifyErr)
				http.Error(w, "ID token validation failed", http.StatusBadRequest)
				return
			}
			emailClaim, _ := claims["email"].(string)
			username := resolveOIDCUsername(r.Context(), h.svc, idToken.Subject, emailClaim)
			groups := extractGroupsFromToken(claims, h.Provider.GroupsClaim)

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
			clearOIDCNonceCookie(w, h.SecureCookies)
			setSessionCookie(w, encoded, h.SecureCookies)
			http.Redirect(w, r, redirect, http.StatusFound)
			return
		}

		// No id_token returned: fall back to the userinfo flow (unchanged).
		userInfo, err := h.getUserInfo(r.Context(), accessToken)
		if err != nil {
			http.Error(w, "Userinfo failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		sub, _ := userInfo["sub"].(string)
		email, _ := userInfo["email"].(string)
		username := resolveOIDCUsername(r.Context(), h.svc, sub, email)

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
		clearOIDCNonceCookie(w, h.SecureCookies)
		setSessionCookie(w, encoded, h.SecureCookies)
		http.Redirect(w, r, redirect, http.StatusFound)
	}
}

// verifyIDToken validates the ID token (signature/JWKS, iss, aud, exp) and the
// nonce. It intentionally does NOT fall back to userinfo on failure: a provider
// that returns an id_token must produce a verifiable one.
func (h *OIDCHandler) verifyIDToken(ctx context.Context, rawIDToken, nonce string) (*oidc.IDToken, map[string]any, error) {
	idToken, err := h.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, nil, fmt.Errorf("verify id token: %w", err)
	}
	if nonce == "" || idToken.Nonce != nonce {
		return nil, nil, fmt.Errorf("id token nonce mismatch")
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return nil, nil, fmt.Errorf("decode id token claims: %w", err)
	}
	return idToken, claims, nil
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
	pkce, err := r.Cookie(oidcPKCECookieName)
	if err != nil || pkce.Value == "" {
		return nil, errMissingPKCEVerifier
	}
	data.Add("code_verifier", pkce.Value)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.Discovery.TokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc token endpoint returned status %d", resp.StatusCode)
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (h *OIDCHandler) getUserInfo(ctx context.Context, accessToken string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.Discovery.UserinfoEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc userinfo endpoint returned status %d", resp.StatusCode)
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func clearOIDCStateCookie(w http.ResponseWriter, secure bool) {
	//nolint:gosec // Secure reflects the effective TLS deployment, derived once at startup
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

func clearOIDCNonceCookie(w http.ResponseWriter, secure bool) {
	//nolint:gosec // Secure reflects the effective TLS deployment, derived once at startup
	http.SetCookie(w, &http.Cookie{
		Name:     oidcNonceCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func clearOIDCPKCECookie(w http.ResponseWriter, secure bool) {
	//nolint:gosec // Secure reflects the effective TLS deployment, derived once at startup
	http.SetCookie(w, &http.Cookie{
		Name:     oidcPKCECookieName,
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
