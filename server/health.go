package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// Check reports whether a dependency is ready. It must respect ctx.
type Check func(ctx context.Context) error

// Health configures /readyz. Checks run concurrently, each bounded by
// Timeout (default 2s).
type Health struct {
	Checks  map[string]Check
	Timeout time.Duration
}

// Check results reported by /readyz.
const (
	statusOK      = "ok"
	statusTimeout = "timeout"
	statusError   = "error"
)

// healthHandler answers GET/HEAD /healthz and /readyz and passes every
// other request to next.
type healthHandler struct {
	health   Health
	next     http.Handler
	draining *atomic.Bool
	version  string
	log      *slog.Logger
}

func (h *healthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		switch r.URL.Path {
		case "/healthz":
			writeJSON(w, http.StatusOK, map[string]string{"status": statusOK, "version": h.version})
			return
		case "/readyz":
			h.ready(w, r)
			return
		}
	}
	h.next.ServeHTTP(w, r)
}

type readiness struct {
	Status string            `json:"status"`
	Reason string            `json:"reason,omitempty"`
	Checks map[string]string `json:"checks"`
}

func (h *healthHandler) ready(w http.ResponseWriter, r *http.Request) {
	if h.draining.Load() {
		writeJSON(w, http.StatusServiceUnavailable,
			readiness{Status: "unavailable", Reason: "shutting down", Checks: map[string]string{}})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.health.Timeout)
	defer cancel()
	type result struct {
		name string
		err  error
	}
	// Buffered so checks that ignore ctx can finish without blocking.
	results := make(chan result, len(h.health.Checks))
	for name, check := range h.health.Checks {
		go func() { results <- result{name, check(ctx)} }()
	}

	res := readiness{Status: statusOK, Checks: make(map[string]string, len(h.health.Checks))}
	for name := range h.health.Checks {
		res.Checks[name] = statusTimeout
	}
	for range h.health.Checks {
		select {
		case rr := <-results:
			switch {
			case rr.err == nil:
				res.Checks[rr.name] = statusOK
			case ctx.Err() != nil:
				res.Checks[rr.name] = statusTimeout
			default:
				// The error stays in the log: /readyz is public.
				res.Checks[rr.name] = statusError
				h.log.Warn("readiness check failed", "check", rr.name, "error", rr.err)
			}
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}

	status := http.StatusOK
	for _, s := range res.Checks {
		if s != statusOK {
			res.Status, status = "unavailable", http.StatusServiceUnavailable
			break
		}
	}
	writeJSON(w, status, res)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v) // values are maps and structs of strings
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}
