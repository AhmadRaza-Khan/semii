package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/semii/semii/db"
	"github.com/semii/semii/validate"
)

func do(t *testing.T, h http.Handler, method, target string, body string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func decodeProblem(t *testing.T, w *httptest.ResponseRecorder) problem {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json (body %s)", ct, w.Body)
	}
	var p problem
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v (%s)", err, w.Body)
	}
	if p.Status != w.Code {
		t.Fatalf("problem status %d != response status %d", p.Status, w.Code)
	}
	return p
}

func ok(w http.ResponseWriter, r *http.Request) error {
	return JSON(w, http.StatusOK, map[string]string{"pattern": RoutePattern(r), "id": r.PathValue("id")})
}

type namedPolicy string

func (p namedPolicy) String() string { return string(p) }
func (p namedPolicy) Authorize(r *http.Request) error {
	if r.Header.Get("X-Allow") == string(p) {
		return nil
	}
	if r.Header.Get("X-Allow") == "" {
		return ErrUnauthenticated
	}
	return ErrForbidden
}

func TestGroupPrefixSplicing(t *testing.T) {
	r := NewRouter(Options{})
	r.Public()
	v1 := r.Group("/v1")
	orders := v1.Group("/orders/")
	orders.HandleFunc("GET /", ok)
	orders.HandleFunc("GET /{id}", ok)
	orders.HandleFunc("POST /{id}/items", ok)
	r.HandleFunc("GET example.com/host", ok)

	cases := []struct{ method, path, pattern, id string }{
		{"GET", "/v1/orders", "GET /v1/orders", ""},
		{"GET", "/v1/orders/42", "GET /v1/orders/{id}", "42"},
		{"POST", "/v1/orders/42/items", "POST /v1/orders/{id}/items", "42"},
		{"GET", "http://example.com/host", "GET example.com/host", ""},
	}
	for _, c := range cases {
		w := do(t, r, c.method, c.path, "")
		if w.Code != 200 {
			t.Errorf("%s %s: status %d %s", c.method, c.path, w.Code, w.Body)
			continue
		}
		var got map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		if got["pattern"] != c.pattern || got["id"] != c.id {
			t.Errorf("%s %s: got %v want pattern %q id %q", c.method, c.path, got, c.pattern, c.id)
		}
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	r := NewRouter(Options{})
	r.HandleFunc("GET /orders", ok).Public()

	w := do(t, r, "GET", "/nope", "")
	if w.Code != 404 {
		t.Fatalf("status %d", w.Code)
	}
	if p := decodeProblem(t, w); p.Code != "not_found" || p.Type != "about:blank" || p.Title != "Not Found" {
		t.Errorf("%+v", p)
	}

	w = do(t, r, "DELETE", "/orders", "")
	if w.Code != 405 {
		t.Fatalf("status %d", w.Code)
	}
	if p := decodeProblem(t, w); p.Code != "method_not_allowed" {
		t.Errorf("%+v", p)
	}
	if allow := w.Header().Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("Allow = %q", allow)
	}

	// ServeMux redirects still pass through.
	r.HandleFunc("GET /dir/", ok).Public()
	if w := do(t, r, "GET", "/dir", ""); w.Code != http.StatusMovedPermanently {
		t.Errorf("redirect status %d", w.Code)
	}
}

func TestPolicyResolution(t *testing.T) {
	r := NewRouter(Options{})
	api := r.Group("/api").Require(namedPolicy("user"))
	api.HandleFunc("GET /me", ok)
	api.HandleFunc("GET /admin", ok).Require(namedPolicy("admin"))
	api.HandleFunc("GET /open", ok).Public()
	nested := api.Group("/nested")
	nested.HandleFunc("GET /x", ok) // inherits api policy
	late := r.Group("/late")        // policy set after registration
	late.HandleFunc("GET /x", ok)
	late.Public()

	cases := []struct {
		path, allow string
		status      int
	}{
		{"/api/me", "", 401},
		{"/api/me", "admin", 403},
		{"/api/me", "user", 200},
		{"/api/admin", "user", 403},
		{"/api/admin", "admin", 200},
		{"/api/open", "", 200},
		{"/api/nested/x", "", 401},
		{"/api/nested/x", "user", 200},
		{"/late/x", "", 200},
	}
	for _, c := range cases {
		w := do(t, r, "GET", c.path, "", "X-Allow", c.allow)
		if w.Code != c.status {
			t.Errorf("%s allow=%q: status %d want %d (%s)", c.path, c.allow, w.Code, c.status, w.Body)
		}
	}
	if err := r.Verify(); err != nil {
		t.Fatal(err)
	}

	infos := r.Routes()
	want := map[string]string{
		"GET /api/me": "user", "GET /api/admin": "admin", "GET /api/open": "public",
		"GET /api/nested/x": "user", "GET /late/x": "public",
	}
	for _, ri := range infos {
		if want[ri.Pattern] != ri.Policy {
			t.Errorf("%s policy %q want %q", ri.Pattern, ri.Policy, want[ri.Pattern])
		}
		if ri.Handler != "web.ok" {
			t.Errorf("%s handler %q", ri.Pattern, ri.Handler)
		}
	}
}

func TestVerifyDenyByDefault(t *testing.T) {
	r := NewRouter(Options{})
	r.HandleFunc("GET /a", ok)
	r.HandleFunc("GET /b", ok).Public()
	r.Group("/g").HandleFunc("POST /c", ok)

	err := r.Verify()
	if err == nil || !strings.Contains(err.Error(), "GET /a") || !strings.Contains(err.Error(), "POST /g/c") ||
		strings.Contains(err.Error(), "GET /b") {
		t.Fatalf("Verify() = %v", err)
	}
	// Fails closed at request time even if Verify was skipped.
	if w := do(t, r, "GET", "/a", ""); w.Code != 403 {
		t.Errorf("unverified route status %d", w.Code)
	}
	for _, ri := range r.Routes() {
		if ri.Pattern == "GET /a" && ri.Policy != "" {
			t.Errorf("policy %q", ri.Policy)
		}
	}
}

func TestVerifyWarnsMethodless(t *testing.T) {
	var buf bytes.Buffer
	r := NewRouter(Options{Env: "development", Logger: slog.New(slog.NewTextHandler(&buf, nil))})
	r.HandleFunc("/any", ok).Public()
	r.Mount("/static", http.NotFoundHandler()).Public()
	if err := r.Verify(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "/any") || strings.Contains(buf.String(), "/static") {
		t.Errorf("log: %s", buf.String())
	}
}

func TestMount(t *testing.T) {
	r := NewRouter(Options{})
	sub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "sub:"+r.URL.Path)
	})
	r.Group("/admin").Mount("/tools", sub).Public()
	w := do(t, r, "GET", "/admin/tools/a/b", "")
	if w.Code != 200 || w.Body.String() != "sub:/a/b" {
		t.Errorf("%d %q", w.Code, w.Body)
	}
	infos := r.Routes()
	if infos[0].Pattern != "/admin/tools/" || infos[0].Handler != "web.TestMount.func1" {
		t.Errorf("%+v", infos[0])
	}
}

func TestHandleHTTPHandler(t *testing.T) {
	r := NewRouter(Options{})
	r.Handle("GET /h", http.NotFoundHandler()).Name("nf").Require(namedPolicy("x"))
	if w := do(t, r, "GET", "/h", ""); w.Code != 401 {
		t.Errorf("status %d", w.Code)
	}
	ri := r.Routes()[0]
	if ri.Name != "nf" || ri.Method != "GET" || ri.Path != "/h" || ri.Policy != "x" {
		t.Errorf("%+v", ri)
	}
	r.Handle("GET /f", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).Require(PolicyFunc(func(*http.Request) error { return nil }))
	if ri := r.Routes()[1]; ri.Policy != "custom" || !strings.HasPrefix(ri.Handler, "web.TestHandleHTTPHandler") {
		t.Errorf("%+v", ri)
	}
}

func TestRegistrationPanics(t *testing.T) {
	r := NewRouter(Options{})
	for name, f := range map[string]func(){
		"bad prefix":    func() { r.Group("v1") },
		"bad path":      func() { r.HandleFunc("GET orders", ok) },
		"nil handler":   func() { r.HandleFunc("GET /x", nil) },
		"nil handle":    func() { r.Handle("GET /x", nil) },
		"empty require": func() { r.Require() },
		"nil policy":    func() { r.HandleFunc("GET /y", ok).Require(nil) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			f()
		}()
	}
}

func TestErrorRendering(t *testing.T) {
	errDomain := errors.New("order is closed")
	var logBuf bytes.Buffer
	newRouter := func(env string) *Router {
		r := NewRouter(Options{Env: env, Logger: slog.New(slog.NewTextHandler(&logBuf, nil))})
		r.MapError(errDomain, http.StatusConflict, "order_closed")
		r.Public()
		return r
	}
	errs := map[string]error{
		"/web":       Conflict("already exists"),
		"/validate":  validate.Errors{{Field: "email", Code: "email", Message: "must be a valid email"}},
		"/notfound":  fmt.Errorf("get order: %w", db.ErrNotFound),
		"/conflict":  db.ErrConflict,
		"/unauth":    ErrUnauthenticated,
		"/forbidden": ErrForbidden,
		"/creds":     ErrInvalidCredentials,
		"/domain":    fmt.Errorf("svc: %w", errDomain),
		"/deadline":  context.DeadlineExceeded,
		"/boom":      errors.New("secret db password leaked"),
		"/wrapped":   fmt.Errorf("outer: %w", Forbidden("nope")),
		"/limit":     TooManyRequests(1500 * 1e6),
		"/proc":      Unprocessable("bad state"),
		"/badreq":    BadRequest("x"),
		"/unauthz":   Unauthorized("token expired"),
		"/nf":        NotFound("x"),
		"/ise":       &Error{Status: 503, Code: "unavailable", Err: errors.New("cause")},
	}
	want := map[string]struct {
		status int
		code   string
	}{
		"/web": {409, "conflict"}, "/validate": {422, "validation_failed"},
		"/notfound": {404, "not_found"}, "/conflict": {409, "conflict"},
		"/unauth": {401, "unauthenticated"}, "/forbidden": {403, "forbidden"},
		"/creds": {401, "invalid_credentials"}, "/domain": {409, "order_closed"},
		"/deadline": {504, "timeout"}, "/boom": {500, "internal_error"},
		"/wrapped": {403, "forbidden"}, "/limit": {429, "rate_limited"},
		"/proc": {422, "unprocessable"}, "/badreq": {400, "bad_request"},
		"/unauthz": {401, "unauthenticated"}, "/nf": {404, "not_found"},
		"/ise": {503, "unavailable"},
	}
	for _, env := range []string{"production", "development"} {
		r := newRouter(env)
		for path, err := range errs {
			err := err
			r.HandleFunc("GET "+path, func(http.ResponseWriter, *http.Request) error { return err })
		}
		for path, w := range want {
			resp := do(t, RequestID()(r), "GET", path, "")
			if resp.Code != w.status {
				t.Errorf("%s %s: status %d want %d", env, path, resp.Code, w.status)
				continue
			}
			p := decodeProblem(t, resp)
			if p.Code != w.code {
				t.Errorf("%s %s: code %q want %q", env, path, p.Code, w.code)
			}
			if p.RequestID == "" || p.RequestID != resp.Header().Get("X-Request-ID") {
				t.Errorf("%s %s: request_id %q", env, path, p.RequestID)
			}
			switch path {
			case "/boom":
				if env == "production" && p.Detail != "internal server error" {
					t.Errorf("production leaked detail %q", p.Detail)
				}
				if env == "development" && !strings.Contains(p.Detail, "secret") {
					t.Errorf("dev detail %q", p.Detail)
				}
			case "/notfound":
				if p.Detail != "not found" {
					t.Errorf("mapped detail leaked context: %q", p.Detail)
				}
			case "/validate":
				if len(p.Errors) != 1 || p.Errors[0].Field != "email" {
					t.Errorf("errors %+v", p.Errors)
				}
			case "/limit":
				if resp.Header().Get("Retry-After") != "2" {
					t.Errorf("Retry-After %q", resp.Header().Get("Retry-After"))
				}
			}
			if path != "/validate" && strings.Contains(resp.Body.String(), `"errors"`) {
				t.Errorf("%s: errors key present when empty", path)
			}
		}
	}
	if !strings.Contains(logBuf.String(), "secret db password leaked") {
		t.Error("500 cause not logged")
	}
}

func TestCanceledWritesNothing(t *testing.T) {
	r := NewRouter(Options{})
	r.HandleFunc("GET /c", func(http.ResponseWriter, *http.Request) error { return context.Canceled }).Public()
	w := do(t, r, "GET", "/c", "")
	if w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" {
		t.Errorf("wrote %d %q", w.Code, w.Body)
	}
}

func TestErrorAfterWriteOnlyLogged(t *testing.T) {
	var logBuf bytes.Buffer
	r := NewRouter(Options{Logger: slog.New(slog.NewTextHandler(&logBuf, nil))})
	r.HandleFunc("GET /x", func(w http.ResponseWriter, _ *http.Request) error {
		_ = JSON(w, 200, "partial")
		return errors.New("late failure")
	}).Public()
	w := do(t, r, "GET", "/x", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `"partial"` {
		t.Errorf("%d %q", w.Code, w.Body)
	}
	if !strings.Contains(logBuf.String(), "late failure") {
		t.Error("error not logged")
	}
}

func TestCustomErrorHandler(t *testing.T) {
	r := NewRouter(Options{ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
		w.WriteHeader(418)
		_, _ = w.Write([]byte(err.Error()))
	}})
	r.HandleFunc("GET /x", func(http.ResponseWriter, *http.Request) error { return errors.New("teapot") }).Public()
	if w := do(t, r, "GET", "/x", ""); w.Code != 418 || w.Body.String() != "teapot" {
		t.Errorf("%d %q", w.Code, w.Body)
	}
}

func TestErrorString(t *testing.T) {
	e := &Error{Status: 404, Code: "not_found", Detail: "order", Err: db.ErrNotFound}
	if e.Error() != "404 not_found: order: not found" || !errors.Is(e, db.ErrNotFound) {
		t.Errorf("%q", e.Error())
	}
}

func TestRootHandlerOwnsUnmatched(t *testing.T) {
	r := NewRouter(Options{})
	r.HandleFunc("GET /api", ok).Public()
	r.Mount("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "spa:"+r.URL.Path)
	})).Public()
	if w := do(t, r, "GET", "/anything", ""); w.Code != 200 || w.Body.String() != "spa:/anything" {
		t.Errorf("%d %q", w.Code, w.Body)
	}
	if w := do(t, r, "GET", "/api", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "GET /api") {
		t.Errorf("%d %q", w.Code, w.Body)
	}
}
