package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/semii/semii/validate"
)

// FieldError is a single field-level validation failure.
type FieldError = validate.FieldError

// Authentication sentinel errors. They live in web so the router can map
// them by default without importing package auth; auth re-exports them as
// auth.ErrUnauthenticated, auth.ErrForbidden and auth.ErrInvalidCredentials.
var (
	ErrUnauthenticated    = errors.New("unauthenticated")
	ErrForbidden          = errors.New("forbidden")
	ErrInvalidCredentials = errors.New("invalid credentials")
)

// Error is an HTTP error rendered as an RFC 9457 problem document.
type Error struct {
	Status int
	Code   string // machine-readable code, snake_case
	Title  string // defaults to http.StatusText(Status)
	Detail string
	Fields []FieldError
	Err    error       // wrapped cause; logged, never serialized
	Header http.Header // extra response headers (e.g. Retry-After)
}

// Error implements error.
func (e *Error) Error() string {
	msg := strconv.Itoa(e.Status) + " " + e.Code
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

// Unwrap returns the wrapped cause.
func (e *Error) Unwrap() error { return e.Err }

// NewError returns an Error with the given status, code and detail.
func NewError(status int, code, detail string) *Error {
	return &Error{Status: status, Code: code, Detail: detail}
}

// BadRequest returns a 400 bad_request error.
func BadRequest(detail string) *Error {
	return NewError(http.StatusBadRequest, "bad_request", detail)
}

// Unauthorized returns a 401 unauthenticated error.
func Unauthorized(detail string) *Error {
	return NewError(http.StatusUnauthorized, "unauthenticated", detail)
}

// Forbidden returns a 403 forbidden error.
func Forbidden(detail string) *Error {
	return NewError(http.StatusForbidden, "forbidden", detail)
}

// NotFound returns a 404 not_found error.
func NotFound(detail string) *Error {
	return NewError(http.StatusNotFound, "not_found", detail)
}

// Conflict returns a 409 conflict error.
func Conflict(detail string) *Error {
	return NewError(http.StatusConflict, "conflict", detail)
}

// Unprocessable returns a 422 error carrying field errors. With fields it
// uses code validation_failed, otherwise unprocessable.
func Unprocessable(detail string, fields ...FieldError) *Error {
	code := "unprocessable"
	if len(fields) > 0 {
		code = "validation_failed"
	}
	e := NewError(http.StatusUnprocessableEntity, code, detail)
	e.Fields = fields
	return e
}

// TooManyRequests returns a 429 rate_limited error with a Retry-After
// header (rounded up to whole seconds) when retryAfter > 0.
func TooManyRequests(retryAfter time.Duration) *Error {
	e := NewError(http.StatusTooManyRequests, "rate_limited", "too many requests")
	if retryAfter > 0 {
		secs := int64((retryAfter + time.Second - 1) / time.Second)
		e.Header = http.Header{"Retry-After": {strconv.FormatInt(secs, 10)}}
	}
	return e
}

// problem is the RFC 9457 wire format.
type problem struct {
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Status    int          `json:"status"`
	Detail    string       `json:"detail,omitempty"`
	Code      string       `json:"code,omitempty"`
	RequestID string       `json:"request_id,omitempty"`
	Errors    []FieldError `json:"errors,omitempty"`
}

// WriteProblem writes e as application/problem+json. It is used by the
// router and middleware and is exported for handlers that need to render
// an error without returning it.
func WriteProblem(w http.ResponseWriter, r *http.Request, e *Error) {
	title := e.Title
	if title == "" {
		title = http.StatusText(e.Status)
	}
	p := problem{
		Type: "about:blank", Title: title, Status: e.Status, Detail: e.Detail,
		Code: e.Code, RequestID: RequestIDFrom(r.Context()), Errors: e.Fields,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(p) // problem contains only strings and ints

	h := w.Header()
	for k, vs := range e.Header {
		h[k] = vs
	}
	h.Del("Content-Length")
	h.Set("Content-Type", "application/problem+json")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(e.Status)
	_, _ = w.Write(buf.Bytes())
}

type errorMapping struct {
	target error
	status int
	code   string
}

// errorRenderer converts handler errors into problem responses.
type errorRenderer struct {
	log      *slog.Logger
	dev      bool
	mappings []errorMapping
}

// resolve turns err into an *Error, or nil if nothing should be written.
func (er *errorRenderer) resolve(r *http.Request, err error) *Error {
	var we *Error
	var verrs validate.Errors
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &we):
		if we.Status >= 500 {
			er.logError(r, err)
		}
		return we
	case errors.As(err, &verrs):
		return Unprocessable("request validation failed", verrs...)
	case errors.As(err, &mbe):
		return NewError(http.StatusRequestEntityTooLarge, "body_too_large",
			"request body exceeds "+strconv.FormatInt(mbe.Limit, 10)+" bytes")
	}
	for _, m := range er.mappings {
		if errors.Is(err, m.target) {
			e := NewError(m.status, m.code, m.target.Error())
			if m.status >= 500 {
				er.logError(r, err)
			}
			return e
		}
	}
	switch {
	case errors.Is(err, context.Canceled):
		return nil // client went away; nobody to answer
	case errors.Is(err, context.DeadlineExceeded):
		er.logError(r, err)
		return NewError(http.StatusGatewayTimeout, "timeout", "request timed out")
	}
	er.logError(r, err)
	detail := "internal server error"
	if er.dev {
		detail = err.Error()
	}
	return NewError(http.StatusInternalServerError, "internal_error", detail)
}

func (er *errorRenderer) logError(r *http.Request, err error) {
	er.log.LogAttrs(r.Context(), slog.LevelError, "handler error",
		slog.String("request_id", RequestIDFrom(r.Context())),
		slog.String("method", r.Method),
		slog.String("route", RoutePattern(r)),
		slog.String("error", err.Error()),
	)
}
