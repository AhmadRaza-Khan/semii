package web_test

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/semii/semii/web"
)

type createOrder struct {
	Item string `json:"item" validate:"required,max=50"`
	Qty  int    `json:"qty" validate:"gte=1"`
}

var errOutOfStock = errors.New("out of stock")

// Example wires a router and the default middleware stack using only
// package web, as generated app.go files do.
func Example() {
	log := slog.New(slog.DiscardHandler) // e.g. slog.NewJSONHandler(os.Stdout, nil)

	r := web.NewRouter(web.Options{Logger: log, Env: "production"})
	r.MapError(errOutOfStock, http.StatusConflict, "out_of_stock")

	r.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) error {
		return web.NoContent(w)
	}).Public()

	orders := r.Group("/orders").Require(web.PolicyFunc(func(r *http.Request) error {
		if r.Header.Get("Authorization") == "" {
			return web.ErrUnauthenticated
		}
		return nil
	}))
	orders.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) error {
		in, err := web.Decode[createOrder](r)
		if err != nil {
			return err
		}
		if in.Qty > 10 {
			return errOutOfStock
		}
		return web.Created(w, "/orders/1", in)
	})

	if err := r.Verify(); err != nil {
		panic(err)
	}
	h := web.Chain(r,
		web.RequestID(), web.RealIP(nil), web.Logger(log), web.Recover(log),
		web.SecurityHeaders("production"), web.CORS(web.CORSConfig{}),
		web.BodyLimit(0), web.Timeout(5*time.Second), web.Gzip(),
	)

	for _, body := range []string{`{"item":"tea","qty":2}`, `{"item":"tea","qty":0}`, `{"item":"tea","qty":11}`} {
		req := httptest.NewRequest("POST", "/orders", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer x")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		fmt.Println(w.Code, w.Header().Get("Content-Type"))
	}
	for _, ri := range r.Routes() {
		fmt.Println(ri.Pattern, ri.Policy)
	}
	// Output:
	// 201 application/json
	// 422 application/problem+json
	// 409 application/problem+json
	// GET /healthz public
	// POST /orders custom
}
