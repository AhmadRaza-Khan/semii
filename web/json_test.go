package web

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type createInput struct {
	Email string          `json:"email" validate:"required,email"`
	Total decimal.Decimal `json:"total" validate:"decimal_gte=0"`
	Notes *string         `json:"notes"`
}

func decodeRouter() *Router {
	r := NewRouter(Options{})
	r.Public()
	r.HandleFunc("POST /in", func(w http.ResponseWriter, r *http.Request) error {
		in, err := Decode[createInput](r)
		if err != nil {
			return err
		}
		return Created(w, "/in/1", in)
	})
	return r
}

func TestDecode(t *testing.T) {
	h := Chain(decodeRouter(), BodyLimit(64))
	cases := []struct {
		name, body, ct string
		status         int
		code           string
	}{
		{"ok", `{"email":"a@b.co","total":"1.5"}`, "application/json", 201, ""},
		{"ok charset", `{"email":"a@b.co","total":"1"}`, "application/json; charset=utf-8", 201, ""},
		{"ok +json", `{"email":"a@b.co","total":"1"}`, "application/merge-patch+json", 201, ""},
		{"no content type", `{}`, "", 415, "unsupported_media_type"},
		{"wrong content type", `{}`, "text/plain", 415, "unsupported_media_type"},
		{"empty", ``, "application/json", 400, "empty_body"},
		{"syntax", `{"email":`, "application/json", 400, "invalid_json"},
		{"bad syntax", `{"email" 1}`, "application/json", 400, "invalid_json"},
		{"unknown field", `{"email":"a@b.co","x":1}`, "application/json", 400, "unknown_field"},
		{"trailing", `{"email":"a@b.co","total":"1"} {}`, "application/json", 400, "invalid_json"},
		{"trailing garbage", `{"email":"a@b.co","total":"1"} x`, "application/json", 400, "invalid_json"},
		{"wrong type", `{"email":5}`, "application/json", 400, "invalid_json"},
		{"wrong root", `[1]`, "application/json", 400, "invalid_json"},
		{"too large", `{"email":"` + strings.Repeat("a", 100) + `"}`, "application/json", 413, "body_too_large"},
		{"invalid", `{"email":"nope","total":"-1"}`, "application/json", 422, "validation_failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var hdr []string
			if c.ct != "" {
				hdr = []string{"Content-Type", c.ct}
			}
			w := do(t, h, "POST", "/in", c.body, hdr...)
			if w.Code != c.status {
				t.Fatalf("status %d want %d: %s", w.Code, c.status, w.Body)
			}
			if c.code == "" {
				if w.Header().Get("Location") != "/in/1" || w.Header().Get("Content-Type") != "application/json" {
					t.Errorf("headers %v", w.Header())
				}
				return
			}
			p := decodeProblem(t, w)
			if p.Code != c.code {
				t.Errorf("code %q want %q", p.Code, c.code)
			}
			if c.code == "validation_failed" && len(p.Errors) != 2 {
				t.Errorf("errors %+v", p.Errors)
			}
			if c.code == "unknown_field" && (len(p.Errors) != 1 || p.Errors[0].Field != "x") {
				t.Errorf("errors %+v", p.Errors)
			}
		})
	}
}

func TestDecodeNonStructAndNoBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`[1,2]`))
	r.Header.Set("Content-Type", "application/json")
	got, err := Decode[[]int](r)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	r = httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Content-Type", "application/json")
	if _, err := Decode[createInput](r); err == nil {
		t.Fatal("want error for no body")
	}
}

func TestJSONEncoding(t *testing.T) {
	w := httptest.NewRecorder()
	if err := JSON(w, 200, map[string]string{"html": "<a>&"}); err != nil {
		t.Fatal(err)
	}
	if w.Body.String() != "{\"html\":\"<a>&\"}\n" {
		t.Errorf("%q", w.Body)
	}
	if err := JSON(httptest.NewRecorder(), 200, math.Inf(1)); err == nil {
		t.Error("want encode error")
	}
	w = httptest.NewRecorder()
	_ = NoContent(w)
	if w.Code != 204 {
		t.Error(w.Code)
	}
}

func TestEncodeFailureBecomes500(t *testing.T) {
	r := NewRouter(Options{})
	r.HandleFunc("GET /x", func(w http.ResponseWriter, _ *http.Request) error {
		return JSON(w, 200, math.NaN())
	}).Public()
	if w := do(t, r, "GET", "/x", ""); w.Code != 500 {
		t.Errorf("status %d", w.Code)
	}
}

func TestOptional(t *testing.T) {
	type patch struct {
		Notes Optional[string] `json:"notes"`
		Count Optional[int]    `json:"count"`
		Tag   Optional[string] `json:"tag"`
	}
	var p patch
	if err := json.Unmarshal([]byte(`{"notes":null,"count":3}`), &p); err != nil {
		t.Fatal(err)
	}
	if !p.Notes.Set || !p.Notes.Null || p.Notes.Ptr() != nil {
		t.Errorf("notes %+v", p.Notes)
	}
	if !p.Count.Set || p.Count.Null || *p.Count.Ptr() != 3 {
		t.Errorf("count %+v", p.Count)
	}
	if p.Tag.Set || p.Tag.Ptr() != nil {
		t.Errorf("tag %+v", p.Tag)
	}
	b, _ := json.Marshal(p)
	if string(b) != `{"notes":null,"count":3,"tag":null}` {
		t.Errorf("%s", b)
	}
	if err := json.Unmarshal([]byte(`{"count":"x"}`), &p); err == nil {
		t.Error("want type error")
	}
}

func TestPathParams(t *testing.T) {
	id := uuid.New()
	r := NewRouter(Options{})
	r.Public()
	r.HandleFunc("GET /u/{id}", func(w http.ResponseWriter, r *http.Request) error {
		got, err := PathUUID(r, "id")
		if err != nil {
			return err
		}
		return JSON(w, 200, got)
	})
	r.HandleFunc("GET /n/{id}", func(w http.ResponseWriter, r *http.Request) error {
		got, err := PathInt64(r, "id")
		if err != nil {
			return err
		}
		return JSON(w, 200, got)
	})
	if w := do(t, r, "GET", "/u/"+id.String(), ""); w.Code != 200 || !strings.Contains(w.Body.String(), id.String()) {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	for _, p := range []string{"/u/nope", "/u/" + strings.ReplaceAll(id.String(), "-", ""), "/n/abc", "/n/99999999999999999999"} {
		w := do(t, r, "GET", p, "")
		if w.Code != 400 || decodeProblem(t, w).Code != "invalid_path_param" {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
	}
	if w := do(t, r, "GET", "/n/-7", ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "-7" {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

type pageQuery struct {
	Limit int `query:"limit"`
}

type listQuery struct {
	pageQuery
	Status  string          `query:"status" validate:"omitempty,oneof=open closed"`
	Active  *bool           `query:"active"`
	Since   time.Time       `query:"since"`
	Owner   uuid.UUID       `query:"owner"`
	IDs     []int64         `query:"id"`
	Min     float64         `query:"min"`
	N       uint8           `query:"n"`
	Total   decimal.Decimal `query:"total"`
	Ignored string
}

func TestDecodeQuery(t *testing.T) {
	owner := uuid.New()
	r := httptest.NewRequest("GET", "/?limit=5&status=open&active=false&since=2024-01-02T03:04:05Z&owner="+owner.String()+
		"&id=1&id=2&min=1.5&n=7&total=9.99&Ignored=x&unknown=1", nil)
	q, err := DecodeQuery[listQuery](r)
	if err != nil {
		t.Fatal(err)
	}
	if q.Limit != 5 || q.Status != "open" || q.Active == nil || *q.Active || q.Since.Year() != 2024 ||
		q.Owner != owner || len(q.IDs) != 2 || q.IDs[1] != 2 || q.Min != 1.5 || q.N != 7 ||
		q.Total.String() != "9.99" || q.Ignored != "" {
		t.Errorf("%+v", q)
	}

	r = httptest.NewRequest("GET", "/?limit=x&active=maybe&since=yesterday&owner=1&id=1&id=z&min=a&n=300", nil)
	_, err = DecodeQuery[listQuery](r)
	e, ok := err.(*Error)
	if !ok || e.Status != 400 || e.Code != "invalid_query" || len(e.Fields) != 7 {
		t.Fatalf("%v %+v", err, e)
	}

	r = httptest.NewRequest("GET", "/?status=bogus", nil)
	if _, err := DecodeQuery[listQuery](r); err == nil || !strings.Contains(err.Error(), "status") {
		t.Errorf("want validation error, got %v", err)
	}
}

func TestDecodeQueryPanicsOnNonStruct(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	_, _ = DecodeQuery[int](httptest.NewRequest("GET", "/", nil))
}
