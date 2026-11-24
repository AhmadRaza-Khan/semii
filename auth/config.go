package auth

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/AhmadRaza-Khan/semii/config"
)

// Strategies.
const (
	StrategySession = "session"
	StrategyJWT     = "jwt"
	StrategyAPIKey  = "apikey"
)

// Config is loaded from AUTH_* variables (embed with envPrefix:"AUTH_").
type Config struct {
	Strategies []string `env:"STRATEGIES" default:"jwt"`
	// AppURL is the frontend origin used in email links and accepted as a
	// CSRF origin for session cookies.
	AppURL string `env:"APP_URL" default:"http://localhost:3000"`

	JWTAlgorithm string `env:"JWT_ALGORITHM" default:"HS256"`
	// JWTSecret signs HS256 tokens; at least 32 bytes.
	JWTSecret config.Secret `env:"JWT_SECRET"`
	// JWTPrivateKey is a PEM PKCS#8 Ed25519 key for EdDSA tokens.
	JWTPrivateKey config.Secret `env:"JWT_PRIVATE_KEY"`
	JWTIssuer     string        `env:"JWT_ISSUER" default:"semii"`
	JWTAudience   string        `env:"JWT_AUDIENCE" default:"semii"`

	AccessTTL       time.Duration `env:"ACCESS_TTL" default:"15m"`
	RefreshTTL      time.Duration `env:"REFRESH_TTL" default:"720h"`
	SessionIdle     time.Duration `env:"SESSION_IDLE" default:"168h"`
	SessionAbsolute time.Duration `env:"SESSION_ABSOLUTE" default:"720h"`

	RequireVerifiedEmail bool `env:"REQUIRE_VERIFIED_EMAIL" default:"false"`
	// StrictRoutes makes a route without a policy fail boot. Turning it off
	// downgrades that to a warning; keep it on outside experiments.
	StrictRoutes bool `env:"STRICT_ROUTES" default:"true"`
	// CheckCommonPasswords rejects the 10,000 most common passwords.
	CheckCommonPasswords bool `env:"CHECK_COMMON_PASSWORDS" default:"true"`
}

// Has reports whether strategy is enabled.
func (c Config) Has(strategy string) bool { return slices.Contains(c.Strategies, strategy) }

// Validate checks the configuration at boot.
func (c Config) Validate() error {
	var errs []error
	if len(c.Strategies) == 0 {
		errs = append(errs, errors.New("AUTH_STRATEGIES: at least one of session, jwt, apikey is required"))
	}
	for _, s := range c.Strategies {
		if s != StrategySession && s != StrategyJWT && s != StrategyAPIKey {
			errs = append(errs, fmt.Errorf("AUTH_STRATEGIES: unknown strategy %q", s))
		}
	}
	if c.Has(StrategyAPIKey) && !c.Has(StrategySession) && !c.Has(StrategyJWT) {
		errs = append(errs, errors.New("AUTH_STRATEGIES: apikey needs session or jwt so users can sign in to manage keys"))
	}
	if u, err := url.Parse(c.AppURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("AUTH_APP_URL: must be an absolute http(s) URL, got %q", c.AppURL))
	}
	if c.Has(StrategyJWT) {
		switch c.JWTAlgorithm {
		case "HS256":
			if len(c.JWTSecret.Reveal()) < 32 {
				errs = append(errs, errors.New("AUTH_JWT_SECRET: must be at least 32 bytes"))
			}
		case "EdDSA":
			if _, err := c.ed25519Key(); err != nil {
				errs = append(errs, fmt.Errorf("AUTH_JWT_PRIVATE_KEY: %w", err))
			}
		default:
			errs = append(errs, fmt.Errorf("AUTH_JWT_ALGORITHM: %q must be HS256 or EdDSA", c.JWTAlgorithm))
		}
		if c.JWTIssuer == "" || c.JWTAudience == "" {
			errs = append(errs, errors.New("AUTH_JWT_ISSUER and AUTH_JWT_AUDIENCE are required"))
		}
	}
	for name, d := range map[string]time.Duration{
		"AUTH_ACCESS_TTL": c.AccessTTL, "AUTH_REFRESH_TTL": c.RefreshTTL,
		"AUTH_SESSION_IDLE": c.SessionIdle, "AUTH_SESSION_ABSOLUTE": c.SessionAbsolute,
	} {
		if d <= 0 {
			errs = append(errs, fmt.Errorf("%s: must be positive", name))
		}
	}
	return errors.Join(errs...)
}

func (c Config) ed25519Key() (ed25519.PrivateKey, error) {
	block, _ := pem.Decode([]byte(c.JWTPrivateKey.Reveal()))
	if block == nil {
		return nil, errors.New("not a PEM block")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("not a PKCS#8 private key")
	}
	ed, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("not an Ed25519 key")
	}
	return ed, nil
}

// AppOrigin is the scheme://host[:port] of AppURL.
func (c Config) AppOrigin() string {
	u, err := url.Parse(c.AppURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
