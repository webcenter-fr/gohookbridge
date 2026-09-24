package domain

type AuthConfig struct {
	Internal InternalConfig
	OIDC     OIDCConfig
}

type InternalConfig struct {
	Enabled bool
	Users   []InternalUser
}

type InternalUser struct {
	Username     string
	PasswordHash string
}

type OIDCConfig struct {
	Enabled   bool
	Providers []OIDCProvider
}
