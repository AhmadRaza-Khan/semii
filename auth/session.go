package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/AhmadRaza-Khan/semii/db"
)

// SessionCookie is the session cookie name.
const SessionCookie = "semii_session"

// touchInterval bounds how often last_seen_at is written.
const touchInterval = time.Minute

// Session is a stored server-side session.
type Session struct {
	UserID     string
	Roles      []string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time // absolute expiry
}

// SessionStore persists sessions by the sha256 hash of their ID.
type SessionStore interface {
	// FindSession returns db.ErrNotFound when no session has idHash.
	FindSession(ctx context.Context, idHash string) (Session, error)
	TouchSession(ctx context.Context, idHash string, at time.Time) error
	DeleteSession(ctx context.Context, idHash string) error
}

// Sessions authenticates the session cookie and issues new cookies.
type Sessions struct {
	store    SessionStore
	idle     time.Duration
	absolute time.Duration
	secure   bool
	origins  []string
	now      func() time.Time
}

// NewSessions returns a session authenticator. Cookies are Secure except
// when env is "development". allowedOrigins (plus AUTH_APP_URL's origin)
// may send cookie-authenticated unsafe requests cross-site.
func NewSessions(store SessionStore, c Config, env string, allowedOrigins ...string) *Sessions {
	origins := append([]string{c.AppOrigin()}, allowedOrigins...)
	return &Sessions{
		store: store, idle: c.SessionIdle, absolute: c.SessionAbsolute,
		secure: env != "development", origins: origins, now: time.Now,
	}
}

// IDHashClaim is the Principal.Claims key holding the current session's ID
// hash, used by logout.
const IDHashClaim = "session_id_hash"

// Authenticate implements Authenticator.
func (s *Sessions) Authenticate(r *http.Request) (*Principal, error) {
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	if !s.sameSite(r) {
		return nil, ErrCSRF
	}
	// An unknown or expired session counts as no session: the request
	// continues anonymously (protected routes still answer 401), so a stale
	// cookie never blocks signing in again.
	idHash := HashToken(c.Value)
	sess, err := s.store.FindSession(r.Context(), idHash)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: find session: %w", err)
	}
	now := s.now()
	if now.After(sess.ExpiresAt) || now.Sub(sess.LastSeenAt) > s.idle {
		if err := s.store.DeleteSession(r.Context(), idHash); err != nil {
			return nil, fmt.Errorf("auth: delete session: %w", err)
		}
		return nil, nil
	}
	if now.Sub(sess.LastSeenAt) >= touchInterval {
		if err := s.store.TouchSession(r.Context(), idHash, now); err != nil {
			return nil, fmt.Errorf("auth: touch session: %w", err)
		}
	}
	return &Principal{
		ID: sess.UserID, Kind: KindUser, Roles: sess.Roles,
		Claims: map[string]any{IDHashClaim: idHash},
	}, nil
}

// sameSite applies the CSRF rule for cookie-authenticated unsafe requests:
// Sec-Fetch-Site must be same-origin or none, or Origin must be allowed.
func (s *Sessions) sameSite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	}
	origin := r.Header.Get("Origin")
	return origin != "" && origin != "null" && slices.Contains(s.origins, origin)
}

// New creates a session ID and the cookie carrying it. Store the returned
// token's Hash; never the raw value.
func (s *Sessions) New() (Token, *http.Cookie, error) {
	tok, err := NewToken()
	if err != nil {
		return Token{}, nil, err
	}
	return tok, &http.Cookie{
		Name: SessionCookie, Value: tok.Raw, Path: "/", HttpOnly: true, Secure: s.secure,
		SameSite: http.SameSiteLaxMode, Expires: s.now().Add(s.absolute), MaxAge: int(s.absolute / time.Second),
	}, nil
}

// ExpiresAt is the absolute expiry for a session created now.
func (s *Sessions) ExpiresAt() time.Time { return s.now().Add(s.absolute) }

// ClearCookie expires the session cookie (logout).
func (s *Sessions) ClearCookie(w http.ResponseWriter) { http.SetCookie(w, s.clearCookie()) }

func (s *Sessions) clearCookie() *http.Cookie {
	return &http.Cookie{Name: SessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(0, 0)}
}

// CookieIDHash returns the hash of the session ID in r's cookie, or "".
func CookieIDHash(r *http.Request) string {
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	return HashToken(c.Value)
}
