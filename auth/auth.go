// Package auth authenticates requests and authorizes routes.
//
// Authenticators turn credentials (a session cookie, a JWT, an API key)
// into a Principal; Middleware runs them and stores the result in the
// request context. Policies attached to routes with web.Router.Require
// decide access: no principal yields 401, a failed policy 403.
//
// The flows that use this package (register, login, refresh, password
// reset, API key management) are generated into the application by
// `semii generate auth`.
package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/AhmadRaza-Khan/semii/logx"
	"github.com/AhmadRaza-Khan/semii/web"
)

// Principal is the authenticated caller.
type Principal struct {
	ID     string
	Kind   string // KindUser or KindAPIKey
	Roles  []string
	Scopes []string
	Claims map[string]any
}

// Principal kinds.
const (
	KindUser   = "user"
	KindAPIKey = "apikey"
)

// HasRole reports whether p has any of roles.
func (p *Principal) HasRole(roles ...string) bool {
	for _, have := range p.Roles {
		for _, want := range roles {
			if have == want {
				return true
			}
		}
	}
	return false
}

// HasScopes reports whether p has every scope in scopes.
func (p *Principal) HasScopes(scopes ...string) bool {
	for _, want := range scopes {
		found := false
		for _, have := range p.Scopes {
			if have == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

type ctxKey struct{}

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the request's principal.
func FromContext(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(*Principal)
	return p, ok && p != nil
}

// Errors. ErrUnauthenticated, ErrForbidden and ErrInvalidCredentials are the
// web package's sentinels, which every web.Router maps to 401/403/401.
// ErrTokenExpired and ErrTokenReused render as 401 with their own codes.
var (
	ErrUnauthenticated    = web.ErrUnauthenticated
	ErrForbidden          = web.ErrForbidden
	ErrInvalidCredentials = web.ErrInvalidCredentials

	ErrTokenExpired = &web.Error{Status: http.StatusUnauthorized, Code: "token_expired",
		Detail: "token has expired", Header: challenge(`error="invalid_token"`)}
	ErrTokenReused = &web.Error{Status: http.StatusUnauthorized, Code: "token_reused",
		Detail: "refresh token was already used; all tokens in its family are revoked",
		Header: challenge(`error="invalid_token"`)}
	ErrCSRF = &web.Error{Status: http.StatusForbidden, Code: "csrf_rejected",
		Detail: "cross-site request rejected"}
)

func challenge(params string) http.Header {
	v := "Bearer"
	if params != "" {
		v += " " + params
	}
	return http.Header{"Www-Authenticate": {v}}
}

// Authenticator extracts a principal from a request.
type Authenticator interface {
	// Authenticate returns (nil, nil) when its credentials are absent and
	// ErrInvalidCredentials (or a *web.Error) when present but invalid.
	Authenticate(r *http.Request) (*Principal, error)
}

// AuthenticatorFunc adapts a function to Authenticator.
type AuthenticatorFunc func(r *http.Request) (*Principal, error)

// Authenticate calls f(r).
func (f AuthenticatorFunc) Authenticate(r *http.Request) (*Principal, error) { return f(r) }

// Middleware tries the authenticators in order; the first principal wins.
// Invalid credentials are answered with 401 immediately (no fall-through
// to the next authenticator). Requests without credentials continue
// without a principal and the route policy decides. A principal already in
// the context (testkit's Request.As) is kept.
func Middleware(as ...Authenticator) web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := FromContext(r.Context()); ok {
				next.ServeHTTP(w, r)
				return
			}
			for _, a := range as {
				p, err := a.Authenticate(r)
				if err != nil {
					writeError(w, r, err)
					return
				}
				if p != nil {
					ctx := WithPrincipal(r.Context(), p)
					web.SetPrincipalID(ctx, p.ID)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var we *web.Error
	switch {
	case errors.As(err, &we):
		web.WriteProblem(w, r, we)
	case errors.Is(err, ErrInvalidCredentials):
		web.WriteProblem(w, r, &web.Error{Status: http.StatusUnauthorized, Code: "invalid_credentials",
			Detail: "invalid credentials", Header: challenge(`error="invalid_token"`)})
	default:
		logx.FromContext(r.Context()).Error("authentication failed", "error", err)
		web.WriteProblem(w, r, web.NewError(http.StatusInternalServerError, "internal_error", "internal server error"))
	}
}

// bearerToken returns the token of an "Authorization: Bearer" header.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}
