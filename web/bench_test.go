package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The release bar requires router overhead within 10% of plain
// http.ServeMux. Both benchmarks route the same table and write the same
// small response.

var benchPaths = []string{"/v1/orders", "/v1/orders/{id}", "/v1/customers/{id}/orders", "/v1/products/{sku}", "/healthz"}

func BenchmarkServeMux(b *testing.B) {
	mux := http.NewServeMux()
	for _, p := range benchPaths {
		mux.HandleFunc("GET "+p, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		})
	}
	benchServe(b, mux)
}

func BenchmarkRouter(b *testing.B) {
	r := NewRouter(Options{})
	r.Public()
	v1 := r.Group("/v1")
	for _, p := range benchPaths {
		g := r
		if p != "/healthz" {
			g, p = v1, p[3:]
		}
		g.HandleFunc("GET "+p, func(w http.ResponseWriter, r *http.Request) error {
			w.WriteHeader(200)
			return nil
		})
	}
	benchServe(b, r)
}

func benchServe(b *testing.B, h http.Handler) {
	req := httptest.NewRequest("GET", "/v1/customers/42/orders", nil)
	w := httptest.NewRecorder()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, req)
	}
}
