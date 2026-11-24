package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/AhmadRaza-Khan/semii/db"
)

const (
	apiKeyPrefix   = "sk_"
	prefixLen      = 8
	keyTouchPeriod = 5 * time.Minute
	base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// APIKey is a stored API key. Only the hash of its secret is stored.
type APIKey struct {
	ID         string
	OwnerID    string
	Scopes     []string
	SecretHash string
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
	LastUsedAt *time.Time
}

// APIKeyStore looks API keys up by their public prefix.
type APIKeyStore interface {
	// FindAPIKey returns db.ErrNotFound when no key has prefix.
	FindAPIKey(ctx context.Context, prefix string) (APIKey, error)
	TouchAPIKey(ctx context.Context, id string, at time.Time) error
}

// NewAPIKey generates a key "sk_<env>_<prefix>_<secret>" with an 8-char
// prefix and a 32-byte base62 secret. Show key once; store prefix and
// secretHash. env is shortened to live (production), test or dev.
func NewAPIKey(env string) (key, prefix, secretHash string, err error) {
	if prefix, err = randomBase62(prefixLen); err != nil {
		return "", "", "", err
	}
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	secret := new(big.Int).SetBytes(b).Text(62)
	return apiKeyPrefix + envTag(env) + "_" + prefix + "_" + secret, prefix, HashToken(secret), nil
}

func envTag(env string) string {
	switch env {
	case "production":
		return "live"
	case "test":
		return "test"
	}
	return "dev"
}

func randomBase62(n int) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(base62Alphabet)))
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = base62Alphabet[v.Int64()]
	}
	return string(out), nil
}

// ParseAPIKey splits a key into its prefix and secret.
func ParseAPIKey(key string) (prefix, secret string, ok bool) {
	parts := strings.Split(key, "_")
	if len(parts) != 4 || parts[0]+"_" != apiKeyPrefix || len(parts[2]) != prefixLen || parts[3] == "" {
		return "", "", false
	}
	return parts[2], parts[3], true
}

// APIKeys authenticates "X-API-Key: sk_..." or "Authorization: Bearer sk_...".
type APIKeys struct {
	store APIKeyStore
	now   func() time.Time
	dummy string
}

// NewAPIKeys returns an API key authenticator.
func NewAPIKeys(store APIKeyStore) *APIKeys {
	return &APIKeys{store: store, now: time.Now, dummy: HashToken("semii-dummy-key")}
}

// APIKeyIDClaim is the Principal.Claims key holding the key's ID.
const APIKeyIDClaim = "api_key_id"

// Authenticate implements Authenticator.
func (a *APIKeys) Authenticate(r *http.Request) (*Principal, error) {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		if tok := bearerToken(r); strings.HasPrefix(tok, apiKeyPrefix) {
			key = tok
		}
	}
	if key == "" {
		return nil, nil
	}
	prefix, secret, ok := ParseAPIKey(key)
	if !ok {
		return nil, ErrInvalidCredentials
	}
	k, err := a.store.FindAPIKey(r.Context(), prefix)
	switch {
	case errors.Is(err, db.ErrNotFound):
		EqualHash(HashToken(secret), a.dummy) // same work as a real compare
		return nil, ErrInvalidCredentials
	case err != nil:
		return nil, fmt.Errorf("auth: find api key: %w", err)
	}
	now := a.now()
	if !EqualHash(HashToken(secret), k.SecretHash) || k.RevokedAt != nil || (k.ExpiresAt != nil && now.After(*k.ExpiresAt)) {
		return nil, ErrInvalidCredentials
	}
	if k.LastUsedAt == nil || now.Sub(*k.LastUsedAt) >= keyTouchPeriod {
		if err := a.store.TouchAPIKey(r.Context(), k.ID, now); err != nil {
			return nil, fmt.Errorf("auth: touch api key: %w", err)
		}
	}
	return &Principal{
		ID: k.OwnerID, Kind: KindAPIKey, Scopes: k.Scopes,
		Claims: map[string]any{APIKeyIDClaim: k.ID},
	}, nil
}
