package testkit

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/AhmadRaza-Khan/semii/auth"
)

type fakeT struct {
	testing.TB
	failed string
}

func (f *fakeT) Helper() {}
func (f *fakeT) Fatalf(format string, args ...any) {
	f.failed = format
	panic(f)
}

// expectFail runs fn against a fake TB and reports whether it failed.
func expectFail(t *testing.T, fn func(tb testing.TB)) {
	t.Helper()
	ft := &fakeT{TB: t}
	func() {
		defer func() {
			if r := recover(); r != nil && r != ft {
				panic(r)
			}
		}()
		fn(ft)
	}()
	if ft.failed == "" {
		t.Error("assertion did not fail")
	}
}

func handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /echo", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["q"] = r.URL.Query().Get("q")
		body["auth"] = r.Header.Get("Authorization")
		if c, err := r.Cookie("sid"); err == nil {
			body["sid"] = c.Value
		}
		if c, err := r.Cookie("extra"); err == nil {
			body["extra"] = c.Value
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "s1", Path: "/"})
	})
	mux.HandleFunc("GET /problem", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"status":422,"code":"validation_failed","errors":[{"field":"email","code":"email"}]}`))
	})
	return mux
}

func TestClientRoundTrip(t *testing.T) {
	c := NewClient(t, handler())
	c.GET("/login").Do().ExpectStatus(200)
	var out map[string]any
	c.POST("/echo?a=1").Query("q", "x").Bearer("tok").Cookie(&http.Cookie{Name: "extra", Value: "e"}).
		JSON(map[string]any{"n": 1, "list": []int{1, 2}, "obj": map[string]any{"k": "v", "z": 0}}).Do().
		ExpectStatus(200).
		ExpectHeader("Content-Type", "application/json").
		ExpectJSON(map[string]any{"n": 1, "q": "x", "auth": "Bearer tok", "sid": "s1", "extra": "e",
			"list": []int{1, 2}, "obj": map[string]any{"k": "v"}}).
		Decode(&out)
	if out["sid"] != "s1" || c.POST("/echo").JSON(`{"raw":true}`).Do().Status() != 200 {
		t.Errorf("%v", out)
	}
	r := c.GET("/problem").Do().ExpectProblem(422, "validation_failed").ExpectFieldError("email", "email")
	if len(r.Body()) == 0 || r.HeaderValue("Content-Type") != "application/problem+json" {
		t.Error("body/header accessors")
	}
	_ = c.PUT("/x").Do()
	_ = c.PATCH("/x").Do()
	_ = c.DELETE("/x").Do()
}

func TestAs(t *testing.T) {
	h := auth.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth.FromContext(r.Context())
		if !ok {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(p.ID))
	}))
	c := NewClient(t, h)
	c.GET("/").Do().ExpectStatus(401)
	if got := string(c.GET("/").As(&auth.Principal{ID: "u1"}).Do().ExpectStatus(200).Body()); got != "u1" {
		t.Errorf("request As: %q", got)
	}
	as := c.As(&auth.Principal{ID: "u2"})
	if got := string(as.GET("/").Do().Body()); got != "u2" {
		t.Errorf("client As: %q", got)
	}
	c.GET("/").Do().ExpectStatus(401) // the original client is unchanged
}

func TestAssertionsFail(t *testing.T) {
	h := handler()
	echo := func(tb testing.TB) *Response {
		return NewClient(tb, h).POST("/echo").JSON(map[string]any{"n": 1, "list": []int{1}}).Do()
	}
	problem := func(tb testing.TB) *Response { return NewClient(tb, h).GET("/problem").Do() }
	for name, fn := range map[string]func(testing.TB){
		"status":       func(tb testing.TB) { echo(tb).ExpectStatus(201) },
		"header":       func(tb testing.TB) { echo(tb).ExpectHeader("X", "y") },
		"json value":   func(tb testing.TB) { echo(tb).ExpectJSON(map[string]any{"n": 2}) },
		"json missing": func(tb testing.TB) { echo(tb).ExpectJSON(map[string]any{"nope": 1}) },
		"json length":  func(tb testing.TB) { echo(tb).ExpectJSON(map[string]any{"list": []int{1, 2}}) },
		"json type":    func(tb testing.TB) { echo(tb).ExpectJSON(map[string]any{"n": map[string]any{}}) },
		"json encode":  func(tb testing.TB) { echo(tb).ExpectJSON(func() {}) },
		"not json":     func(tb testing.TB) { NewClient(tb, h).GET("/login").Do().ExpectJSON(map[string]any{}) },
		"problem ct":   func(tb testing.TB) { echo(tb).ExpectProblem(200, "x") },
		"problem code": func(tb testing.TB) { problem(tb).ExpectProblem(422, "other") },
		"field error":  func(tb testing.TB) { problem(tb).ExpectFieldError("name", "required") },
		"decode":       func(tb testing.TB) { var n int; echo(tb).Decode(&n) },
		"encode body":  func(tb testing.TB) { NewClient(tb, h).POST("/echo").JSON(make(chan int)) },
		"problem decode": func(tb testing.TB) {
			NewClient(tb, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/problem+json")
				_, _ = w.Write([]byte("nope"))
			})).GET("/").Do().ExpectFieldError("a", "b")
		},
	} {
		t.Run(name, func(t *testing.T) { expectFail(t, fn) })
	}
}

func TestSecureCookiesRoundTrip(t *testing.T) {
	h := http.NewServeMux()
	h.HandleFunc("GET /set", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "s", Value: "v", Path: "/", Secure: true, HttpOnly: true})
	})
	h.HandleFunc("GET /get", func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("s")
		if err != nil {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(c.Value))
	})
	c := NewClient(t, h)
	c.GET("/set").Do()
	if got := string(c.GET("/get").Do().ExpectStatus(200).Body()); got != "v" {
		t.Errorf("%q", got)
	}
}
