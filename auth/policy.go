package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/AhmadRaza-Khan/semii/web"
)

// Policy authorizes a request given its principal (nil when the request is
// unauthenticated). It implements web.Policy, and its name is shown by
// `semii routes`.
type Policy struct {
	name  string
	check func(ctx context.Context, p *Principal, r *http.Request) error
}

// NewPolicy builds a custom policy. fn should return Unauthenticated() when
// p is nil and Forbidden(...) when p is not allowed.
func NewPolicy(name string, fn func(ctx context.Context, p *Principal, r *http.Request) error) Policy {
	return Policy{name: name, check: fn}
}

// Authorize implements web.Policy.
func (pol Policy) Authorize(r *http.Request) error {
	p, _ := FromContext(r.Context())
	return pol.check(r.Context(), p, r)
}

// String names the policy.
func (pol Policy) String() string { return pol.name }

// Unauthenticated is the 401 returned when a policy needs a principal.
func Unauthenticated() *web.Error {
	return &web.Error{Status: http.StatusUnauthorized, Code: "unauthenticated",
		Detail: "authentication required", Err: ErrUnauthenticated, Header: challenge("")}
}

// Forbidden is the 403 returned when a principal fails a policy.
func Forbidden(detail string) *web.Error {
	return &web.Error{Status: http.StatusForbidden, Code: "forbidden", Detail: detail, Err: ErrForbidden}
}

// Authenticated requires any principal.
func Authenticated() Policy {
	return NewPolicy("authenticated", func(_ context.Context, p *Principal, _ *http.Request) error {
		if p == nil {
			return Unauthenticated()
		}
		return nil
	})
}

// Role requires any of roles.
func Role(roles ...string) Policy {
	return NewPolicy("role:"+strings.Join(roles, "|"), func(_ context.Context, p *Principal, _ *http.Request) error {
		switch {
		case p == nil:
			return Unauthenticated()
		case !p.HasRole(roles...):
			return Forbidden("requires role " + strings.Join(roles, " or "))
		}
		return nil
	})
}

// Scope requires all of scopes.
func Scope(scopes ...string) Policy {
	return NewPolicy("scope:"+strings.Join(scopes, "+"), func(_ context.Context, p *Principal, _ *http.Request) error {
		switch {
		case p == nil:
			return Unauthenticated()
		case !p.HasScopes(scopes...):
			return Forbidden("requires scope " + strings.Join(scopes, " and "))
		}
		return nil
	})
}

// Kind requires a principal of kind k (KindUser or KindAPIKey).
func Kind(k string) Policy {
	return NewPolicy("kind:"+k, func(_ context.Context, p *Principal, _ *http.Request) error {
		switch {
		case p == nil:
			return Unauthenticated()
		case p.Kind != k:
			return Forbidden("requires a " + k + " principal")
		}
		return nil
	})
}

func names(ps []Policy) string {
	n := make([]string, len(ps))
	for i, p := range ps {
		n[i] = p.name
	}
	return strings.Join(n, ",")
}

// Any passes when at least one policy passes. When all fail it returns 401
// for an unauthenticated request and the last 403 otherwise.
func Any(ps ...Policy) Policy {
	return NewPolicy("any("+names(ps)+")", func(ctx context.Context, p *Principal, r *http.Request) error {
		var last error = Forbidden("no policy matched")
		for _, pol := range ps {
			err := pol.check(ctx, p, r)
			if err == nil {
				return nil
			}
			last = err
		}
		if p == nil {
			return Unauthenticated()
		}
		if errors.Is(last, ErrUnauthenticated) {
			return Forbidden("no policy matched")
		}
		return last
	})
}

// All passes when every policy passes, returning the first failure.
func All(ps ...Policy) Policy {
	return NewPolicy("all("+names(ps)+")", func(ctx context.Context, p *Principal, r *http.Request) error {
		for _, pol := range ps {
			if err := pol.check(ctx, p, r); err != nil {
				return err
			}
		}
		return nil
	})
}

var _ web.Policy = Policy{}
