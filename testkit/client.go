// Package testkit provides an in-process HTTP client with fluent
// assertions and per-test databases for integration tests.
package testkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// baseURL is the origin requests are attributed to; it only matters for
// the cookie jar.
var baseURL = &url.URL{Scheme: "http", Host: "example.test"}

// Client sends requests straight to a handler through httptest, keeping a
// cookie jar across requests so session flows work.
type Client struct {
	t   testing.TB
	h   http.Handler
	jar http.CookieJar
}

// NewClient returns a Client for h.
func NewClient(t testing.TB, h http.Handler) *Client {
	jar, _ := cookiejar.New(nil) // never fails with nil options
	return &Client{t: t, h: h, jar: jar}
}

// GET starts a GET request.
func (c *Client) GET(path string) *Request { return c.newRequest(http.MethodGet, path) }

// POST starts a POST request.
func (c *Client) POST(path string) *Request { return c.newRequest(http.MethodPost, path) }

// PUT starts a PUT request.
func (c *Client) PUT(path string) *Request { return c.newRequest(http.MethodPut, path) }

// PATCH starts a PATCH request.
func (c *Client) PATCH(path string) *Request { return c.newRequest(http.MethodPatch, path) }

// DELETE starts a DELETE request.
func (c *Client) DELETE(path string) *Request { return c.newRequest(http.MethodDelete, path) }

func (c *Client) newRequest(method, path string) *Request {
	return &Request{c: c, method: method, path: path, header: http.Header{}, query: url.Values{}}
}

// Request is a request being built. Its methods return it for chaining.
type Request struct {
	c       *Client
	method  string
	path    string
	header  http.Header
	query   url.Values
	body    []byte
	cookies []*http.Cookie
}

// JSON sets v, encoded as JSON, as the body. A []byte or string is sent
// verbatim, which allows malformed bodies in tests.
func (r *Request) JSON(v any) *Request {
	switch b := v.(type) {
	case []byte:
		r.body = b
	case string:
		r.body = []byte(b)
	default:
		var err error
		if r.body, err = json.Marshal(v); err != nil {
			r.c.t.Helper()
			r.c.t.Fatalf("testkit: encode request body: %v", err)
		}
	}
	r.header.Set("Content-Type", "application/json")
	return r
}

// Query adds a query parameter.
func (r *Request) Query(k, v string) *Request { r.query.Add(k, v); return r }

// Header sets a request header.
func (r *Request) Header(k, v string) *Request { r.header.Set(k, v); return r }

// Bearer sets an Authorization: Bearer header.
func (r *Request) Bearer(token string) *Request { return r.Header("Authorization", "Bearer "+token) }

// Cookie adds a cookie to this request only.
func (r *Request) Cookie(c *http.Cookie) *Request { r.cookies = append(r.cookies, c); return r }

// Do sends the request through the handler.
func (r *Request) Do() *Response {
	t := r.c.t
	t.Helper()
	target := r.path
	if len(r.query) > 0 {
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + r.query.Encode()
	}
	var body io.Reader
	if r.body != nil {
		body = bytes.NewReader(r.body)
	}
	req := httptest.NewRequest(r.method, target, body)
	req.Header = r.header.Clone()
	for _, ck := range r.c.jar.Cookies(baseURL) {
		req.AddCookie(ck)
	}
	for _, ck := range r.cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	r.c.h.ServeHTTP(rec, req)
	res := rec.Result()
	if cs := res.Cookies(); len(cs) > 0 {
		r.c.jar.SetCookies(baseURL, cs)
	}
	return &Response{t: t, req: r.method + " " + target, rec: rec}
}

// Response is a recorded response with chainable assertions. Failed
// assertions stop the test, printing the response body.
type Response struct {
	t   testing.TB
	req string
	rec *httptest.ResponseRecorder
}

// Status returns the status code.
func (r *Response) Status() int { return r.rec.Code }

// Body returns the raw body.
func (r *Response) Body() []byte { return r.rec.Body.Bytes() }

// HeaderValue returns a response header.
func (r *Response) HeaderValue(k string) string { return r.rec.Header().Get(k) }

func (r *Response) fail(format string, args ...any) {
	r.t.Helper()
	r.t.Fatalf("%s: %s\nresponse %d: %s", r.req, fmt.Sprintf(format, args...), r.rec.Code, r.rec.Body.String())
}

// ExpectStatus asserts the status code.
func (r *Response) ExpectStatus(code int) *Response {
	r.t.Helper()
	if r.rec.Code != code {
		r.fail("status = %d, want %d", r.rec.Code, code)
	}
	return r
}

// ExpectHeader asserts a response header value.
func (r *Response) ExpectHeader(k, v string) *Response {
	r.t.Helper()
	if got := r.rec.Header().Get(k); got != v {
		r.fail("header %s = %q, want %q", k, got, v)
	}
	return r
}

// ExpectJSON asserts that the body contains subset: objects may have extra
// keys, arrays must match element by element. subset is compared after a
// JSON round trip, so structs and maps both work.
func (r *Response) ExpectJSON(subset any) *Response {
	r.t.Helper()
	var got any
	if err := json.Unmarshal(r.rec.Body.Bytes(), &got); err != nil {
		r.fail("body is not JSON: %v", err)
	}
	b, err := json.Marshal(subset)
	if err != nil {
		r.fail("encode expected JSON: %v", err)
	}
	var want any
	_ = json.Unmarshal(b, &want)
	if path, ok := containsJSON(got, want, "$"); !ok {
		r.fail("JSON mismatch at %s: want %s", path, b)
	}
	return r
}

// containsJSON reports whether got contains want, returning the first
// mismatching path.
func containsJSON(got, want any, path string) (string, bool) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return path, false
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok {
				return path + "." + k + " (missing)", false
			}
			if p, ok := containsJSON(gv, wv, path+"."+k); !ok {
				return p, false
			}
		}
		return "", true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return path + " (length)", false
		}
		for i := range w {
			if p, ok := containsJSON(g[i], w[i], fmt.Sprintf("%s[%d]", path, i)); !ok {
				return p, false
			}
		}
		return "", true
	}
	return path, reflect.DeepEqual(got, want)
}

// Problem is a decoded problem+json body.
type Problem struct {
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Status    int          `json:"status"`
	Detail    string       `json:"detail"`
	Code      string       `json:"code"`
	RequestID string       `json:"request_id"`
	Errors    []FieldError `json:"errors"`
}

// FieldError is one entry of Problem.Errors.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r *Response) problem() Problem {
	r.t.Helper()
	if ct := r.rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		r.fail("Content-Type = %q, want application/problem+json", ct)
	}
	var p Problem
	if err := json.Unmarshal(r.rec.Body.Bytes(), &p); err != nil {
		r.fail("decode problem: %v", err)
	}
	return p
}

// ExpectProblem asserts a problem+json response with status and code.
func (r *Response) ExpectProblem(status int, code string) *Response {
	r.t.Helper()
	r.ExpectStatus(status)
	if p := r.problem(); p.Code != code {
		r.fail("problem code = %q, want %q", p.Code, code)
	}
	return r
}

// ExpectFieldError asserts that the problem lists field with rule code.
func (r *Response) ExpectFieldError(field, code string) *Response {
	r.t.Helper()
	p := r.problem()
	for _, fe := range p.Errors {
		if fe.Field == field && fe.Code == code {
			return r
		}
	}
	r.fail("no field error %s/%s", field, code)
	return r
}

// Decode unmarshals the JSON body into v.
func (r *Response) Decode(v any) *Response {
	r.t.Helper()
	if err := json.Unmarshal(r.rec.Body.Bytes(), v); err != nil {
		r.fail("decode body into %T: %v", v, err)
	}
	return r
}
