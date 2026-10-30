package web

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CORSConfig configures the CORS middleware. CORS is disabled when
// AllowedOrigins is empty.
type CORSConfig struct {
	// AllowedOrigins are matched exactly against the Origin header.
	// "*" allows any origin and cannot be combined with AllowCredentials.
	AllowedOrigins []string
	// AllowedMethods default to GET, POST, PUT, PATCH, DELETE.
	AllowedMethods []string
	// AllowedHeaders default to Authorization, Content-Type, X-Request-ID,
	// X-API-Key.
	AllowedHeaders []string
	// ExposedHeaders are readable by browser clients on actual responses.
	ExposedHeaders []string
	// AllowCredentials sends Access-Control-Allow-Credentials: true.
	AllowCredentials bool
	// MaxAge is how long preflight results may be cached. Default 10m.
	MaxAge time.Duration
}

// Validate reports configuration errors.
func (c CORSConfig) Validate() error {
	if c.AllowCredentials && slices.Contains(c.AllowedOrigins, "*") {
		return errors.New(`web: CORS AllowedOrigins "*" cannot be combined with AllowCredentials`)
	}
	return nil
}

// CORS handles cross-origin requests and answers preflights with 204.
// It panics if cfg is invalid (see CORSConfig.Validate); validate the
// config at load time to report the error gracefully.
func CORS(cfg CORSConfig) Middleware {
	if err := cfg.Validate(); err != nil {
		panic(err)
	}
	if len(cfg.AllowedOrigins) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	methods := cfg.AllowedMethods
	if len(methods) == 0 {
		methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	}
	headers := cfg.AllowedHeaders
	if len(headers) == 0 {
		headers = []string{"Authorization", "Content-Type", "X-Request-ID", "X-API-Key"}
	}
	maxAge := cfg.MaxAge
	if maxAge <= 0 {
		maxAge = 10 * time.Minute
	}
	var (
		anyOrigin    = slices.Contains(cfg.AllowedOrigins, "*")
		allowMethods = strings.Join(methods, ", ")
		allowHeaders = strings.Join(headers, ", ")
		expose       = strings.Join(cfg.ExposedHeaders, ", ")
		maxAgeSecs   = strconv.Itoa(int(maxAge / time.Second))
	)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Add("Vary", "Origin")
			origin := r.Header.Get("Origin")
			if origin == "" || !(anyOrigin || slices.Contains(cfg.AllowedOrigins, origin)) {
				next.ServeHTTP(w, r)
				return
			}
			allowOrigin := origin
			if anyOrigin {
				allowOrigin = "*"
			}
			h.Set("Access-Control-Allow-Origin", allowOrigin)
			if cfg.AllowCredentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				h.Set("Access-Control-Allow-Methods", allowMethods)
				h.Set("Access-Control-Allow-Headers", allowHeaders)
				h.Set("Access-Control-Max-Age", maxAgeSecs)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if expose != "" {
				h.Set("Access-Control-Expose-Headers", expose)
			}
			next.ServeHTTP(w, r)
		})
	}
}
