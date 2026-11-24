package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
)

// Token is a random secret and the hash stored in its place (A3).
type Token struct {
	Raw  string // given to the client once
	Hash string // stored; sha256 hex
}

// NewToken returns 32 random bytes, base64url-encoded, and their hash. Use
// it for session IDs, refresh tokens and one-time reset/verify tokens.
func NewToken() (Token, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return Token{}, err
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	return Token{Raw: raw, Hash: HashToken(raw)}, nil
}

// HashToken is the storage form of a raw token: sha256, hex-encoded.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// EqualHash compares two hashes in constant time (A5).
func EqualHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
