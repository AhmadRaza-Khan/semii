package auth

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Leeway tolerated on exp, nbf and iat.
const Leeway = 30 * time.Second

// JWT issues and verifies access tokens with one configured algorithm.
// Verification never trusts the token's "alg" header.
type JWT struct {
	method    jwt.SigningMethod
	signKey   any
	verifyKey any
	issuer    string
	audience  string
	ttl       time.Duration
	now       func() time.Time
}

// NewJWT builds a JWT from c (HS256 with AUTH_JWT_SECRET, or EdDSA with
// AUTH_JWT_PRIVATE_KEY).
func NewJWT(c Config) (*JWT, error) {
	j := &JWT{issuer: c.JWTIssuer, audience: c.JWTAudience, ttl: c.AccessTTL, now: time.Now}
	switch c.JWTAlgorithm {
	case "HS256", "":
		secret := []byte(c.JWTSecret.Reveal())
		if len(secret) < 32 {
			return nil, errors.New("auth: AUTH_JWT_SECRET must be at least 32 bytes")
		}
		j.method, j.signKey, j.verifyKey = jwt.SigningMethodHS256, secret, secret
	case "EdDSA":
		key, err := c.ed25519Key()
		if err != nil {
			return nil, fmt.Errorf("auth: AUTH_JWT_PRIVATE_KEY: %w", err)
		}
		j.method, j.signKey, j.verifyKey = jwt.SigningMethodEdDSA, key, key.Public().(ed25519.PublicKey)
	default:
		return nil, fmt.Errorf("auth: unsupported JWT algorithm %q", c.JWTAlgorithm)
	}
	if j.ttl <= 0 {
		j.ttl = 15 * time.Minute
	}
	return j, nil
}

type accessClaims struct {
	jwt.RegisteredClaims
	Roles []string `json:"roles,omitempty"`
}

// Issue returns a signed access token for subject and its expiry.
func (j *JWT) Issue(subject string, roles []string) (string, time.Time, error) {
	now := j.now()
	exp := now.Add(j.ttl)
	jti, _ := NewToken()
	claims := accessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: subject, Issuer: j.issuer, Audience: jwt.ClaimStrings{j.audience},
			IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp), ID: jti.Raw[:22],
		},
		Roles: roles,
	}
	signed, err := jwt.NewWithClaims(j.method, claims).SignedString(j.signKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: sign: %w", err)
	}
	return signed, exp, nil
}

// Parse verifies token and returns its principal. Expired tokens yield
// ErrTokenExpired; every other failure ErrInvalidCredentials.
func (j *JWT) Parse(token string) (*Principal, error) {
	var claims accessClaims
	_, err := jwt.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) { return j.verifyKey, nil },
		jwt.WithValidMethods([]string{j.method.Alg()}),
		jwt.WithIssuer(j.issuer), jwt.WithAudience(j.audience),
		jwt.WithLeeway(Leeway), jwt.WithExpirationRequired(), jwt.WithIssuedAt(),
		jwt.WithTimeFunc(j.now),
	)
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return nil, ErrTokenExpired
	case err != nil:
		return nil, ErrInvalidCredentials
	case claims.Subject == "":
		return nil, ErrInvalidCredentials
	}
	return &Principal{
		ID: claims.Subject, Kind: KindUser, Roles: claims.Roles,
		Claims: map[string]any{"jti": claims.ID},
	}, nil
}

// Authenticate implements Authenticator for "Authorization: Bearer <jwt>".
// API keys sent as bearer tokens (sk_...) are left to the API key
// authenticator.
func (j *JWT) Authenticate(r *http.Request) (*Principal, error) {
	tok := bearerToken(r)
	if tok == "" || strings.HasPrefix(tok, apiKeyPrefix) {
		return nil, nil
	}
	return j.Parse(tok)
}
