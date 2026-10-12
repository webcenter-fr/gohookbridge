package handler

// Pointer semantics for update requests: a nil field is preserved, a non-nil
// zero value clears the field. The server never accepts identity, provenance,
// token, or mode fields from the request body.

// ChannelUpdateRequest is the PATCH-style channel update body.
type ChannelUpdateRequest struct {
	Description         *string   `json:"description"`
	WebhookSecret       *string   `json:"webhook_secret"`
	AllowedIPs          *[]string `json:"allowed_ips"`
	MaxBodySize         *int      `json:"max_body_size"`
	MessageTTLSeconds   *int      `json:"message_ttl_seconds"`
	EncryptionMode      *string   `json:"encryption_mode"`
	EncryptionKey       *string   `json:"encryption_key"`
	EncryptionPublicKey *string   `json:"encryption_public_key"`
}

// GlobalConfigUpdateRequest is the global-config update body.
type GlobalConfigUpdateRequest struct {
	Server   *ServerConfigUpdateRequest `json:"server"`
	Defaults *DefaultsUpdateRequest     `json:"defaults"`
}

// ServerConfigUpdateRequest mirrors ServerConfig without the session secret
// (which is never settable through the API).
type ServerConfigUpdateRequest struct {
	MaxBodySize            *int      `json:"max_body_size"`
	BehindReverseProxy     *bool     `json:"behind_reverse_proxy"`
	CORSOrigin             *string   `json:"cors_origin"`
	Footer                 *string   `json:"footer"`
	TrustedProxyCIDRs      *[]string `json:"trusted_proxy_cidrs"`
	RateLimitEnabled       *bool     `json:"rate_limit_enabled"`
	RateLimitRequests      *int      `json:"rate_limit_requests"`
	RateLimitWindowSeconds *int      `json:"rate_limit_window_seconds"`
	BanEnabled             *bool     `json:"ban_enabled"`
	BanMaxUniqueFailures   *int      `json:"ban_max_unique_failures"`
	BanWindowSeconds       *int      `json:"ban_window_seconds"`
	BanDurationSeconds     *int      `json:"ban_duration_seconds"`
	RequireKnownChannels   *bool     `json:"require_known_channels"`
	LoginThrottleAttempts  *int      `json:"login_throttle_attempts"`
	LoginThrottleCooldown  *int      `json:"login_throttle_cooldown_seconds"`
	// SessionSecret is accepted only to reject an attempt to set it.
	SessionSecret *string `json:"session_secret"`
}

// DefaultsUpdateRequest is the default-channel-config update body.
type DefaultsUpdateRequest struct {
	WebhookSecret     *string   `json:"webhook_secret"`
	AllowedIPs        *[]string `json:"allowed_ips"`
	MessageTTLSeconds *int      `json:"message_ttl_seconds"`
}

// AccessTokenResponse is the safe projection of a channel access token.
type AccessTokenResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"created_at"`
}

// ChannelResponse is the safe channel projection: secrets and token hashes are
// replaced by *_configured booleans and never emitted.
type ChannelResponse struct {
	ID                      string                `json:"id"`
	Description             string                `json:"description,omitempty"`
	CreatedBy               string                `json:"created_by,omitempty"`
	WebhookSecretConfigured bool                  `json:"webhook_secret_configured"`
	AllowedIPs              []string              `json:"allowed_ips,omitempty"`
	MaxBodySize             int                   `json:"max_body_size,omitempty"`
	MessageTTLSeconds       int                   `json:"message_ttl_seconds,omitempty"`
	EncryptionMode          string                `json:"encryption_mode,omitempty"`
	EncryptionPublicKey     string                `json:"encryption_public_key,omitempty"`
	EncryptionKeyConfigured bool                  `json:"encryption_key_configured"`
	AccessMode              string                `json:"access_mode,omitempty"`
	AccessTokens            []AccessTokenResponse `json:"access_tokens,omitempty"`
}

// DefaultsResponse is the safe projection of the default channel config.
type DefaultsResponse struct {
	WebhookSecretConfigured bool     `json:"webhook_secret_configured"`
	AllowedIPs              []string `json:"allowed_ips,omitempty"`
	MessageTTLSeconds       int      `json:"message_ttl_seconds,omitempty"`
}

// GlobalConfigResponse is the safe global-config projection.
type GlobalConfigResponse struct {
	Server   ServerConfigResponse `json:"server"`
	Defaults DefaultsResponse     `json:"defaults"`
}

// ServerConfigResponse is the safe server-config projection.
type ServerConfigResponse struct {
	MaxBodySize        int    `json:"max_body_size"`
	BehindReverseProxy bool   `json:"behind_reverse_proxy"`
	CORSOrigin         string `json:"cors_origin"`
	Footer             string `json:"footer"`

	TrustedProxyCIDRs    []string `json:"trusted_proxy_cidrs,omitempty"`
	RequireKnownChannels bool     `json:"require_known_channels"`

	RateLimitEnabled       bool `json:"rate_limit_enabled"`
	RateLimitRequests      int  `json:"rate_limit_requests"`
	RateLimitWindowSeconds int  `json:"rate_limit_window_seconds"`

	BanEnabled           bool `json:"ban_enabled"`
	BanMaxUniqueFailures int  `json:"ban_max_unique_failures"`
	BanWindowSeconds     int  `json:"ban_window_seconds"`
	BanDurationSeconds   int  `json:"ban_duration_seconds"`

	LoginThrottleAttempts int `json:"login_throttle_attempts"`
	LoginThrottleCooldown int `json:"login_throttle_cooldown_seconds"`
}
