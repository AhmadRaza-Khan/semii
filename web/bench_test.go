package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The release bar requires router overhead within 10% of plain
// http.ServeMux. Every pair routes the same table and writes the same
// small JSON response:
//
//   - Bare: no middleware. This isolates dispatch, policy checks and the
//     pooled writer that tracks whether an error-returning handler wrote;
//     it is the worst case (~50 ns, about 15% of an empty handler).
//   - Stack: RequestID, Logger and Recover on both sides, as in a
//     generated app. semii's writers already track writes there, so the
//     router adds only dispatch and policy checks. This is the release-bar
//     measurement.

var benchPaths = []string{"/v1/orders", "/v1/orders/{id}", "/v1/customers/{id}/orders", "/v1/products/{sku}", "/healthz"}

var benchBody = []byte(`{"id":"42","status":"paid"}`)

func benchMux() *http.ServeMux {
	mux := http.NewServeMux()
	for _, p := range benchPaths {
		mux.HandleFunc("GET "+p, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write(benchBody)
		})
	}
	return mux
}

func benchRouter() *Router {
	r := NewRouter(Options{})
	r.Public()
	v1 := r.Group("/v1")
	for _, p := range benchPaths {
		g := r
		if p != "/healthz" {
			g, p = v1, p[3:]
		}
		g.HandleFunc("GET "+p, func(w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, err := w.Write(benchBody)
			return err
		})
	}
	return r
}

func benchStack(h http.Handler) http.Handler {
	log := slog.New(slog.DiscardHandler)
	return Chain(h, RequestID(), Logger(log), Recover(log))
}

func BenchmarkServeMux(b *testing.B)      { benchServe(b, benchMux()) }
func BenchmarkRouter(b *testing.B)        { benchServe(b, benchRouter()) }
func BenchmarkServeMuxStack(b *testing.B) { benchServe(b, benchStack(benchMux())) }
func BenchmarkRouterStack(b *testing.B)   { benchServe(b, benchStack(benchRouter())) }

// discardWriter stands in for a connection: it keeps the header map and
// drops the body, so neither side pays for a growing recorder buffer.
type discardWriter struct {
	h      http.Header
	status int
}

func (w *discardWriter) Header() http.Header         { return w.h }
func (w *discardWriter) WriteHeader(code int)        { w.status = code }
func (w *discardWriter) Write(b []byte) (int, error) { return len(b), nil }

func benchServe(b *testing.B, h http.Handler) {
	req := httptest.NewRequest("GET", "/v1/customers/42/orders", nil)
	w := &discardWriter{h: http.Header{}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, req)
	}
}
