package web

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestChainOrder(t *testing.T) {
	var order []string
	mw := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { order = append(order, "h") }), mw("a"), mw("b"))
	do(t, h, "GET", "/", "")
	if strings.Join(order, ",") != "a,b,h" {
		t.Error(order)
	}

	// Group middleware runs outside the policy check, parent first.
	order = nil
	r := NewRouter(Options{})
	g := r.Group("/g", mw("g1")).Group("", mw("g2")).Require(PolicyFunc(func(*http.Request) error {
		order = append(order, "policy")
		return nil
	}))
	g.HandleFunc("GET /x", func(http.ResponseWriter, *http.Request) error { order = append(order, "h"); return nil })
	do(t, r, "GET", "/g/x", "")
	if strings.Join(order, ",") != "g1,g2,policy,h" {
		t.Error(order)
	}
}

func TestRequestID(t *testing.T) {
	var seen string
	h := RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = RequestIDFrom(r.Context()) }))

	w := do(t, h, "GET", "/", "", "X-Request-ID", "abc-123_.X")
	if seen != "abc-123_.X" || w.Header().Get("X-Request-ID") != seen {
		t.Errorf("accepted id %q", seen)
	}
	for _, bad := range []string{"", "has space", "semi;colon", strings.Repeat("a", 65), "ünï"} {
		w := do(t, h, "GET", "/", "", "X-Request-ID", bad)
		if seen == bad || len(seen) != 26 || w.Header().Get("X-Request-ID") != seen {
			t.Errorf("bad %q -> %q", bad, seen)
		}
	}
	if RequestIDFrom(context.Background()) != "" {
		t.Error("expected empty")
	}
}

func TestULID(t *testing.T) {
	ts := time.UnixMilli(1469918176385) // reference timestamp from the ULID spec
	id := newULID(ts)
	if len(id) != 26 || !strings.HasPrefix(id, "01ARYZ6S41") {
		t.Errorf("%s", id)
	}
	if newULID(ts) == id {
		t.Error("not random")
	}
	a, b := newULID(time.UnixMilli(1)), newULID(time.UnixMilli(2))
	if a[:10] >= b[:10] {
		t.Error("not time ordered")
	}
}

func TestRealIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	var got netip.Addr
	capture := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = ClientIP(r) })
	cases := []struct {
		name, remote, xff, xri, want string
		trusted                      []netip.Prefix
	}{
		{"untrusted ignores headers", "1.2.3.4:5", "9.9.9.9", "8.8.8.8", "1.2.3.4", trusted},
		{"no trusted default", "10.0.0.1:5", "9.9.9.9", "", "10.0.0.1", nil},
		{"trusted uses xff", "10.0.0.1:5", "9.9.9.9", "", "9.9.9.9", trusted},
		{"rightmost untrusted", "10.0.0.1:5", "6.6.6.6, 9.9.9.9, 10.1.1.1", "", "9.9.9.9", trusted},
		{"all trusted -> leftmost", "10.0.0.1:5", "10.2.2.2, 10.1.1.1", "", "10.2.2.2", trusted},
		{"malformed xff", "10.0.0.1:5", "garbage", "", "10.0.0.1", trusted},
		{"x-real-ip", "10.0.0.1:5", "", "7.7.7.7", "7.7.7.7", trusted},
		{"ipv6 peer", "[::1]:80", "", "", "::1", trusted},
		{"mapped v4", "[::ffff:1.2.3.4]:80", "", "", "1.2.3.4", nil},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if c.xri != "" {
			r.Header.Set("X-Real-IP", c.xri)
		}
		RealIP(c.trusted)(capture).ServeHTTP(httptest.NewRecorder(), r)
		if got.String() != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "5.6.7.8"
	if ClientIP(r).String() != "5.6.7.8" {
		t.Error("ClientIP fallback without port")
	}
}

func TestLogger(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	r := NewRouter(Options{})
	r.Public()
	r.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) error {
		SetPrincipalID(r.Context(), "user-1")
		return JSON(w, 200, "ok")
	})
	r.HandleFunc("GET /fail", func(http.ResponseWriter, *http.Request) error { return errors.New("x") })
	r.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) error { return NoContent(w) })
	r.HandleFunc("GET /silent", func(http.ResponseWriter, *http.Request) error { return nil })
	h := Chain(r, RequestID(), RealIP(nil), Logger(log))

	lines := func() []map[string]any {
		var out []map[string]any
		for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if l == "" {
				continue
			}
			var m map[string]any
			_ = json.Unmarshal([]byte(l), &m)
			if m["msg"] == "request" {
				out = append(out, m)
			}
		}
		buf.Reset()
		return out
	}

	do(t, h, "GET", "/orders/7", "")
	l := lines()
	if len(l) != 1 {
		t.Fatalf("lines %v", l)
	}
	m := l[0]
	if m["level"] != "INFO" || m["route"] != "GET /orders/{id}" || m["status"] != 200.0 || m["principal_id"] != "user-1" ||
		m["method"] != "GET" || m["client_ip"] != "192.0.2.1" || m["bytes_out"] != 5.0 || len(m["request_id"].(string)) != 26 {
		t.Errorf("%v", m)
	}
	if _, ok := m["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms %v", m["duration_ms"])
	}

	do(t, h, "GET", "/fail", "")
	if l := lines(); l[0]["level"] != "ERROR" || l[0]["status"] != 500.0 {
		t.Errorf("%v", l)
	}
	do(t, h, "GET", "/missing", "")
	if l := lines(); l[0]["level"] != "WARN" || l[0]["status"] != 404.0 || l[0]["route"] != "" {
		t.Errorf("%v", l)
	}
	do(t, h, "GET", "/silent", "")
	if l := lines(); l[0]["status"] != 200.0 {
		t.Errorf("%v", l)
	}
	do(t, h, "GET", "/healthz", "")
	if l := lines(); len(l) != 0 {
		t.Errorf("healthz logged: %v", l)
	}
}

func TestRecover(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("kaboom") }), RequestID(), Recover(log))
	w := do(t, h, "GET", "/", "")
	if w.Code != 500 || decodeProblem(t, w).Code != "internal_error" {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	if !strings.Contains(buf.String(), "kaboom") || !strings.Contains(buf.String(), "goroutine") {
		t.Errorf("log: %s", buf.String())
	}

	h = Recover(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(202)
		panic("after write")
	}))
	if w := do(t, h, "GET", "/", ""); w.Code != 202 || w.Body.Len() != 0 {
		t.Errorf("%d %q", w.Code, w.Body)
	}

	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler {
			t.Errorf("recovered %v", rec)
		}
	}()
	Recover(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func TestSecurityHeaders(t *testing.T) {
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	w := do(t, SecurityHeaders("development")(noop), "GET", "/", "")
	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
	}
	for k, v := range want {
		if w.Header().Get(k) != v {
			t.Errorf("%s = %q", k, w.Header().Get(k))
		}
	}
	if w.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS outside production")
	}
	w = do(t, SecurityHeaders("production")(noop), "GET", "/", "")
	if w.Header().Get("Strict-Transport-Security") != "max-age=63072000; includeSubDomains" {
		t.Error("missing HSTS")
	}
}

func TestCORS(t *testing.T) {
	noop := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })

	if w := do(t, CORS(CORSConfig{})(noop), "GET", "/", "", "Origin", "https://a.com"); w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Vary") != "" {
		t.Error("disabled CORS emitted headers")
	}

	h := CORS(CORSConfig{AllowedOrigins: []string{"https://a.com"}, AllowCredentials: true, ExposedHeaders: []string{"X-Request-ID"}})(noop)
	w := do(t, h, "GET", "/", "", "Origin", "https://a.com")
	if w.Header().Get("Access-Control-Allow-Origin") != "https://a.com" || w.Header().Get("Access-Control-Allow-Credentials") != "true" ||
		w.Header().Get("Access-Control-Expose-Headers") != "X-Request-ID" || w.Header().Get("Vary") != "Origin" {
		t.Errorf("simple: %v", w.Header())
	}
	w = do(t, h, "GET", "/", "", "Origin", "https://evil.com")
	if w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Vary") != "Origin" {
		t.Errorf("disallowed: %v", w.Header())
	}
	w = do(t, h, "OPTIONS", "/", "", "Origin", "https://a.com", "Access-Control-Request-Method", "PATCH")
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Methods") != "GET, POST, PUT, PATCH, DELETE" ||
		w.Header().Get("Access-Control-Allow-Headers") != "Authorization, Content-Type, X-Request-ID, X-API-Key" ||
		w.Header().Get("Access-Control-Max-Age") != "600" {
		t.Errorf("preflight: %d %v", w.Code, w.Header())
	}

	h = CORS(CORSConfig{AllowedOrigins: []string{"*"}, MaxAge: time.Hour, AllowedMethods: []string{"GET"}})(noop)
	w = do(t, h, "OPTIONS", "/", "", "Origin", "https://x.com", "Access-Control-Request-Method", "GET")
	if w.Header().Get("Access-Control-Allow-Origin") != "*" || w.Header().Get("Access-Control-Max-Age") != "3600" ||
		w.Header().Get("Access-Control-Allow-Methods") != "GET" {
		t.Errorf("wildcard: %v", w.Header())
	}

	bad := CORSConfig{AllowedOrigins: []string{"*"}, AllowCredentials: true}
	if bad.Validate() == nil {
		t.Error("Validate accepted * with credentials")
	}
	defer func() {
		if recover() == nil {
			t.Error("CORS did not panic on invalid config")
		}
	}()
	CORS(bad)
}

func TestBodyLimit(t *testing.T) {
	r := decodeRouter()
	big := `{"email":"a@b.co","total":"1","notes":"` + strings.Repeat("x", 200) + `"}`
	ct := []string{"Content-Type", "application/json"}

	if w := do(t, Chain(r, BodyLimit(100)), "POST", "/in", big, ct...); w.Code != 413 {
		t.Errorf("status %d", w.Code)
	}
	// An inner limit replaces the outer one, so routes can raise it.
	if w := do(t, Chain(r, BodyLimit(100), BodyLimit(1000)), "POST", "/in", big, ct...); w.Code != 201 {
		t.Errorf("raised: status %d %s", w.Code, w.Body)
	}
	if w := do(t, Chain(r, BodyLimit(0)), "POST", "/in", big, ct...); w.Code != 201 {
		t.Errorf("default: status %d", w.Code)
	}
	// Handlers reading raw bodies past the limit get a MaxBytesError -> 413.
	raw := NewRouter(Options{})
	raw.HandleFunc("POST /raw", func(_ http.ResponseWriter, r *http.Request) error {
		_, err := io.ReadAll(r.Body)
		return err
	}).Public()
	if w := do(t, Chain(raw, BodyLimit(10)), "POST", "/raw", big); w.Code != 413 || decodeProblem(t, w).Code != "body_too_large" {
		t.Errorf("raw: %d", w.Code)
	}
}

func TestTimeout(t *testing.T) {
	r := NewRouter(Options{})
	r.HandleFunc("GET /slow", func(_ http.ResponseWriter, r *http.Request) error {
		select {
		case <-r.Context().Done():
			return r.Context().Err()
		case <-time.After(time.Second):
			return nil
		}
	}).Public()
	if w := do(t, Chain(r, Timeout(10*time.Millisecond)), "GET", "/slow", ""); w.Code != 504 {
		t.Errorf("status %d", w.Code)
	}
	var dl time.Time
	Timeout(0)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { dl, _ = r.Context().Deadline() })).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if d := time.Until(dl); d < 29*time.Second || d > 30*time.Second {
		t.Errorf("default deadline in %v", d)
	}
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestGzip(t *testing.T) {
	large := strings.Repeat("a", 2000)
	handler := func(ct string, body string, status int) http.Handler {
		return Gzip()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			if status != 0 {
				w.WriteHeader(status)
			}
			// Write in small chunks to exercise buffering.
			for i := 0; i < len(body); i += 300 {
				_, _ = io.WriteString(w, body[i:min(i+300, len(body))])
			}
		}))
	}
	ae := []string{"Accept-Encoding", "br, gzip;q=0.8"}

	w := do(t, handler("application/json", large, 201), "GET", "/", "", ae...)
	if w.Code != 201 || w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Vary") != "Accept-Encoding" ||
		gunzip(t, w.Body.Bytes()) != large {
		t.Errorf("json: %d %v", w.Code, w.Header())
	}
	if w := do(t, handler("", large, 0), "GET", "/", "", ae...); w.Header().Get("Content-Encoding") != "gzip" ||
		!strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("sniffed: %v", w.Header())
	}
	noGzip := []struct {
		name string
		h    http.Handler
		hdr  []string
	}{
		{"small", handler("application/json", "tiny", 0), ae},
		{"binary", handler("image/png", large, 0), ae},
		{"sse", handler("text/event-stream", large, 0), ae},
		{"not accepted", handler("application/json", large, 0), nil},
		{"q=0", handler("application/json", large, 0), []string{"Accept-Encoding", "gzip;q=0"}},
		{"bad ct", handler("garbage;;", large, 0), ae},
	}
	for _, c := range noGzip {
		w := do(t, c.h, "GET", "/", "", c.hdr...)
		if w.Header().Get("Content-Encoding") != "" || w.Body.Len() == 0 {
			t.Errorf("%s: compressed or empty: %v", c.name, w.Header())
		}
	}
	pre := Gzip()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "br")
		_, _ = io.WriteString(w, large)
	}))
	if w := do(t, pre, "GET", "/", "", ae...); w.Header().Get("Content-Encoding") != "br" || w.Body.String() != large {
		t.Error("re-encoded already encoded body")
	}
	if w := do(t, handler("application/json", "", 204), "GET", "/", "", ae...); w.Code != 204 || w.Header().Get("Content-Encoding") != "" {
		t.Errorf("204: %v", w.Header())
	}
	if w := do(t, handler("application/json", "", 0), "GET", "/", "", ae...); w.Code != 200 || w.Body.Len() != 0 {
		t.Errorf("empty: %d", w.Code)
	}
}

func TestGzipFlush(t *testing.T) {
	h := Gzip()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "chunk1")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, "chunk2")
	}))
	w := do(t, h, "GET", "/", "", "Accept-Encoding", "gzip")
	if !w.Flushed || w.Header().Get("Content-Encoding") != "gzip" || gunzip(t, w.Body.Bytes()) != "chunk1chunk2" {
		t.Errorf("flushed=%v %v", w.Flushed, w.Header())
	}
}

type hijackRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (h *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return nil, nil, nil
}

func TestResponseWriterPreservesInterfaces(t *testing.T) {
	rec := &hijackRecorder{ResponseRecorder: httptest.NewRecorder()}
	rw := wrapWriter(rec)
	if wrapWriter(rw) != rw {
		t.Error("double wrap")
	}
	rc := http.NewResponseController(rw)
	if err := rc.Flush(); err != nil || !rec.Flushed || rw.Status() != 200 {
		t.Errorf("flush: %v", err)
	}
	if _, _, err := rc.Hijack(); err != nil || !rec.hijacked {
		t.Errorf("hijack: %v", err)
	}
	if err := rc.SetWriteDeadline(time.Time{}); !errors.Is(err, http.ErrNotSupported) {
		t.Errorf("deadline: %v", err)
	}
	if _, _, err := wrapWriter(httptest.NewRecorder()).Hijack(); err == nil {
		t.Error("hijack on non-hijacker succeeded")
	}
	gw := &gzipWriter{ResponseWriter: rec}
	if _, _, err := gw.Hijack(); err != nil || gw.Unwrap() != rec {
		t.Error("gzip hijack")
	}

	rw = wrapWriter(httptest.NewRecorder())
	rw.WriteHeader(http.StatusContinue)
	if rw.written {
		t.Error("1xx counted as written")
	}
}

func TestErrorAfterWriteBehindGzip(t *testing.T) {
	r := NewRouter(Options{})
	r.HandleFunc("GET /x", func(w http.ResponseWriter, _ *http.Request) error {
		_ = JSON(w, 200, "partial")
		return errors.New("late failure")
	}).Public()
	r.HandleFunc("GET /y", func(http.ResponseWriter, *http.Request) error {
		return Conflict("nope")
	}).Public()
	h := Chain(r, Gzip())
	if w := do(t, h, "GET", "/x", "", "Accept-Encoding", "gzip"); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `"partial"` {
		t.Errorf("%d %q", w.Code, w.Body)
	}
	if w := do(t, h, "GET", "/y", "", "Accept-Encoding", "gzip"); w.Code != 409 {
		t.Errorf("%d %q", w.Code, w.Body)
	}
}
