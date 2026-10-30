package web

import (
	"context"
	"net"
	"net/http"
	"net/netip"

	"github.com/semii/semii/validate"
)

type ctxKey struct{}

// reqState is the per-request record shared by semii middleware and the
// router. It is created once per request by the outermost semii component
// that sees the request and is mutated only by the goroutine serving it.
type reqState struct {
	requestID   string
	clientIP    netip.Addr
	pattern     string // matched route pattern, set by the router
	principalID string // set by auth middleware via SetPrincipalID
	validator   *validate.Validator
}

func stateFrom(ctx context.Context) *reqState {
	s, _ := ctx.Value(ctxKey{}).(*reqState)
	return s
}

// withState returns the request's state, attaching a new one if absent.
func withState(r *http.Request) (*reqState, *http.Request) {
	if s := stateFrom(r.Context()); s != nil {
		return s, r
	}
	s := &reqState{}
	return s, r.WithContext(context.WithValue(r.Context(), ctxKey{}, s))
}

// RequestIDFrom returns the request ID set by the RequestID middleware, or
// "" if there is none.
func RequestIDFrom(ctx context.Context) string {
	if s := stateFrom(ctx); s != nil {
		return s.requestID
	}
	return ""
}

// ClientIP returns the client address resolved by the RealIP middleware,
// falling back to the direct peer address from r.RemoteAddr.
func ClientIP(r *http.Request) netip.Addr {
	if s := stateFrom(r.Context()); s != nil && s.clientIP.IsValid() {
		return s.clientIP
	}
	return peerAddr(r)
}

func peerAddr(r *http.Request) netip.Addr {
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return ap.Addr().Unmap()
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, _ := netip.ParseAddr(host)
	return a.Unmap()
}

// SetPrincipalID records the authenticated principal for the request log
// line. It is called by authentication middleware; it is a no-op when the
// request did not pass through RequestID, RealIP or Logger.
func SetPrincipalID(ctx context.Context, id string) {
	if s := stateFrom(ctx); s != nil {
		s.principalID = id
	}
}

// RoutePattern returns the full pattern (including group prefixes) of the
// route handling r, or "" if none matched. Middleware outside the router
// sees it after the request returns, provided RequestID, RealIP or Logger
// ran first to attach the shared request state.
func RoutePattern(r *http.Request) string {
	if s := stateFrom(r.Context()); s != nil && s.pattern != "" {
		return s.pattern
	}
	return r.Pattern
}

func validatorFrom(ctx context.Context) *validate.Validator {
	if s := stateFrom(ctx); s != nil && s.validator != nil {
		return s.validator
	}
	return validate.Default
}
