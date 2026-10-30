package web

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// Chain wraps h with mw; mw[0] is the outermost.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// RequestID accepts an incoming X-Request-ID of 1-64 characters from
// [A-Za-z0-9._-], otherwise generates a ULID. The ID is stored in the
// request context (see RequestIDFrom) and echoed in the response header.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s, r := withState(r)
			id := r.Header.Get("X-Request-ID")
			if !validRequestID(id) {
				id = newULID(time.Now())
			}
			s.requestID = id
			w.Header().Set("X-Request-ID", id)
			next.ServeHTTP(w, r)
		})
	}
}

func validRequestID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newULID returns a 26-character ULID: 48-bit millisecond timestamp
// followed by 80 random bits, Crockford base32 encoded.
func newULID(t time.Time) string {
	var b [16]byte
	ms := uint64(t.UnixMilli())
	b[0], b[1], b[2] = byte(ms>>40), byte(ms>>32), byte(ms>>24)
	b[3], b[4], b[5] = byte(ms>>16), byte(ms>>8), byte(ms)
	_, _ = rand.Read(b[6:]) // crypto/rand.Read never fails since Go 1.24

	hi, lo := binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:])
	var out [26]byte
	// 128 bits -> 26 base32 digits (130 bits, top 2 zero), least significant last.
	for i := 25; i >= 0; i-- {
		out[i] = crockford[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out[:])
}

// RealIP resolves the client address. X-Forwarded-For and X-Real-IP are
// honoured only when the direct peer is in trusted; the rightmost
// X-Forwarded-For entry that is not itself trusted is used. With no trusted
// prefixes (the default) the peer address is always used. The result is
// available through ClientIP.
func RealIP(trusted []netip.Prefix) Middleware {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s, r := withState(r)
			ip := peerAddr(r)
			if len(trusted) > 0 && ip.IsValid() && isTrusted(ip) {
				if fwd, ok := forwardedFor(r, isTrusted); ok {
					ip = fwd
				}
			}
			s.clientIP = ip
			next.ServeHTTP(w, r)
		})
	}
}

func forwardedFor(r *http.Request, isTrusted func(netip.Addr) bool) (netip.Addr, bool) {
	var hops []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(h, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return netip.Addr{}, false // malformed chain: trust nothing in it
		}
		a = a.Unmap()
		if !isTrusted(a) || i == 0 {
			return a, true
		}
	}
	if a, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
		return a.Unmap(), true
	}
	return netip.Addr{}, false
}

// SecurityHeaders sets conservative API security headers. HSTS is only sent
// when env is "production".
func SecurityHeaders(env string) Middleware {
	hsts := env == "production"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// DefaultBodyLimit is the BodyLimit applied when n <= 0.
const DefaultBodyLimit int64 = 1 << 20

// BodyLimit caps request bodies at n bytes (DefaultBodyLimit if n <= 0)
// using http.MaxBytesReader; reading past it yields a 413 from Decode.
// An inner BodyLimit (for example on a route group) replaces an outer one,
// so a group can raise the limit set globally.
func BodyLimit(n int64) Middleware {
	if n <= 0 {
		n = DefaultBodyLimit
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody {
				orig := r.Body
				if lb, ok := orig.(*limitedBody); ok {
					orig = lb.orig
				}
				r2 := *r // shallow copy so the caller's request is not mutated
				r2.Body = &limitedBody{orig: orig, ReadCloser: http.MaxBytesReader(w, orig, n)}
				r = &r2
			}
			next.ServeHTTP(w, r)
		})
	}
}

type limitedBody struct {
	io.ReadCloser
	orig io.ReadCloser
}

// DefaultTimeout is the Timeout applied when d <= 0.
const DefaultTimeout = 30 * time.Second

// Timeout sets a deadline on the request context (DefaultTimeout if
// d <= 0). It does not buffer or cut off responses; handlers must respect
// ctx. A handler returning context.DeadlineExceeded is answered with 504.
func Timeout(d time.Duration) Middleware {
	if d <= 0 {
		d = DefaultTimeout
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
