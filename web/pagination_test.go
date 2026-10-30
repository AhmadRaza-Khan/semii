package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCursorRoundTrip(t *testing.T) {
	ts := time.Date(2024, 5, 6, 7, 8, 9, 123, time.UTC)
	id := uuid.New()
	cur := EncodeCursor(ts, id)

	p, err := ParseCursor(httptest.NewRequest("GET", "/?limit=50&cursor="+cur, nil), 20, 100)
	if err != nil {
		t.Fatal(err)
	}
	if p.Limit != 50 || p.Cursor == nil {
		t.Fatalf("%+v", p)
	}
	var gotTS time.Time
	var gotID uuid.UUID
	if err := p.Cursor.Scan(&gotTS, &gotID); err != nil {
		t.Fatal(err)
	}
	if !gotTS.Equal(ts) || gotID != id {
		t.Errorf("%v %v", gotTS, gotID)
	}
	if err := p.Cursor.Scan(&gotTS); err == nil {
		t.Error("want count mismatch error")
	}
	var n int
	if err := p.Cursor.Scan(&n, &gotID); err == nil {
		t.Error("want type mismatch error")
	}
}

func TestParseCursorErrors(t *testing.T) {
	p, err := ParseCursor(httptest.NewRequest("GET", "/", nil), 20, 100)
	if err != nil || p.Limit != 20 || p.Cursor != nil {
		t.Fatalf("%+v %v", p, err)
	}
	for _, q := range []string{"limit=0", "limit=101", "limit=x", "cursor=!!", "cursor=e30", "cursor=" + strings.Repeat("A", 8)} {
		_, err := ParseCursor(httptest.NewRequest("GET", "/?"+q, nil), 20, 100)
		e, ok := err.(*Error)
		if !ok || e.Status != 400 {
			t.Errorf("%s: %v", q, err)
		}
	}
}

func TestNewCursorPage(t *testing.T) {
	keys := func(n int) []any { return []any{n} }
	pg := NewCursorPage([]int{1, 2, 3}, 2, keys)
	if len(pg.Data) != 2 || pg.NextCursor == nil {
		t.Fatalf("%+v", pg)
	}
	p, err := ParseCursor(httptest.NewRequest("GET", "/?cursor="+*pg.NextCursor, nil), 20, 100)
	var last int
	if err != nil || p.Cursor.Scan(&last) != nil || last != 2 {
		t.Errorf("last %d err %v", last, err)
	}
	if pg := NewCursorPage([]int{1}, 2, keys); pg.NextCursor != nil || len(pg.Data) != 1 {
		t.Errorf("%+v", pg)
	}
	if pg := NewCursorPage[int](nil, 2, keys); pg.Data == nil {
		t.Error("nil data")
	}
}

func TestEncodeCursorPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	EncodeCursor(make(chan int))
}

func TestOffset(t *testing.T) {
	p, err := ParseOffset(httptest.NewRequest("GET", "/?page=3&per_page=10", nil), 20, 100)
	if err != nil || p.Page != 3 || p.PerPage != 10 || p.Offset() != 20 {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = ParseOffset(httptest.NewRequest("GET", "/", nil), 20, 100)
	if err != nil || p.Page != 1 || p.PerPage != 20 || p.Offset() != 0 {
		t.Fatalf("%+v %v", p, err)
	}
	for _, q := range []string{"page=0", "page=x", "per_page=101", "per_page=0"} {
		if _, err := ParseOffset(httptest.NewRequest("GET", "/?"+q, nil), 20, 100); err == nil {
			t.Errorf("%s: no error", q)
		}
	}
	pg := NewOffsetPage[string](nil, p, 134)
	if pg.Data == nil || pg.Total != 134 || pg.Page != 1 || pg.PerPage != 20 {
		t.Errorf("%+v", pg)
	}
}
