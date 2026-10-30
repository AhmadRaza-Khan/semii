package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// Cursor is a decoded keyset pagination cursor. Keys holds the sort key
// values of the last item of the previous page, in sort order.
type Cursor struct {
	Keys      []json.RawMessage `json:"k"`
	Direction string            `json:"d"`
}

// Scan decodes the cursor keys into dest, in order:
//
//	var createdAt time.Time; var id uuid.UUID
//	err := p.Cursor.Scan(&createdAt, &id)
//
// A count or type mismatch returns a 400 invalid_cursor error.
func (c *Cursor) Scan(dest ...any) error {
	if len(dest) != len(c.Keys) {
		return invalidCursor()
	}
	for i, d := range dest {
		if err := json.Unmarshal(c.Keys[i], d); err != nil {
			return invalidCursor()
		}
	}
	return nil
}

// CursorParams are the parsed ?limit= and ?cursor= parameters. Cursor is
// nil on the first page.
type CursorParams struct {
	Limit  int
	Cursor *Cursor
}

// CursorPage is the response envelope for cursor-paginated lists.
type CursorPage[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

// ParseCursor reads ?limit= (default defLimit, 1..maxLimit) and ?cursor=.
// Out-of-range limits and malformed cursors return 400 errors.
func ParseCursor(r *http.Request, defLimit, maxLimit int) (CursorParams, error) {
	q := r.URL.Query()
	limit, err := parseLimit(q.Get("limit"), "limit", defLimit, maxLimit)
	if err != nil {
		return CursorParams{}, err
	}
	p := CursorParams{Limit: limit}
	if raw := q.Get("cursor"); raw != "" {
		b, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			return CursorParams{}, invalidCursor()
		}
		var c Cursor
		if err := json.Unmarshal(b, &c); err != nil || c.Direction != "next" || len(c.Keys) == 0 {
			return CursorParams{}, invalidCursor()
		}
		p.Cursor = &c
	}
	return p, nil
}

func invalidCursor() *Error {
	return NewError(http.StatusBadRequest, "invalid_cursor", "cursor is malformed or expired")
}

// EncodeCursor returns an opaque cursor for the given sort key values. It
// panics if a key cannot be JSON-encoded, which is a programming error.
func EncodeCursor(keys ...any) string {
	raw := make([]json.RawMessage, len(keys))
	for i, k := range keys {
		b, err := json.Marshal(k)
		if err != nil {
			panic(fmt.Sprintf("web: EncodeCursor key %d (%T): %v", i, k, err))
		}
		raw[i] = b
	}
	b, _ := json.Marshal(Cursor{Keys: raw, Direction: "next"})
	return base64.RawURLEncoding.EncodeToString(b)
}

// NewCursorPage builds a page from items fetched with LIMIT limit+1: if
// more than limit items were returned, the extra one is dropped and
// NextCursor encodes keys(last item on the page). Data is never nil.
func NewCursorPage[T any](items []T, limit int, keys func(T) []any) CursorPage[T] {
	if items == nil {
		items = []T{}
	}
	if len(items) <= limit {
		return CursorPage[T]{Data: items}
	}
	items = items[:limit]
	next := EncodeCursor(keys(items[len(items)-1])...)
	return CursorPage[T]{Data: items, NextCursor: &next}
}

// OffsetParams are the parsed ?page= and ?per_page= parameters.
type OffsetParams struct {
	Page    int
	PerPage int
}

// Offset returns the number of rows to skip.
func (p OffsetParams) Offset() int { return (p.Page - 1) * p.PerPage }

// OffsetPage is the response envelope for offset-paginated lists.
type OffsetPage[T any] struct {
	Data    []T   `json:"data"`
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Total   int64 `json:"total"`
}

// NewOffsetPage builds an offset page; Data is never nil.
func NewOffsetPage[T any](items []T, p OffsetParams, total int64) OffsetPage[T] {
	if items == nil {
		items = []T{}
	}
	return OffsetPage[T]{Data: items, Page: p.Page, PerPage: p.PerPage, Total: total}
}

// ParseOffset reads ?page= (default 1) and ?per_page= (default
// defPerPage, 1..maxPerPage).
func ParseOffset(r *http.Request, defPerPage, maxPerPage int) (OffsetParams, error) {
	q := r.URL.Query()
	per, err := parseLimit(q.Get("per_page"), "per_page", defPerPage, maxPerPage)
	if err != nil {
		return OffsetParams{}, err
	}
	page := 1
	if s := q.Get("page"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return OffsetParams{}, paramError("page", "must be an integer >= 1")
		}
		page = n
	}
	return OffsetParams{Page: page, PerPage: per}, nil
}

func parseLimit(s, name string, def, max int) (int, error) {
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > max {
		return 0, paramError(name, fmt.Sprintf("must be an integer between 1 and %d", max))
	}
	return n, nil
}

func paramError(name, msg string) *Error {
	e := NewError(http.StatusBadRequest, "invalid_query", name+" "+msg)
	e.Fields = []FieldError{{Field: name, Code: "invalid_query", Message: msg}}
	return e
}
