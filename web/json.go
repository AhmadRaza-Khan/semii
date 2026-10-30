package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// Decode reads a JSON request body into a T and validates it.
//
// It responds (via the returned error) with 415 unless Content-Type is
// application/json or a +json type, 400 for malformed JSON, unknown fields
// or trailing data, 413 when the body exceeds the BodyLimit, and 422 with
// field errors when validation fails.
func Decode[T any](r *http.Request) (T, error) {
	var v T
	if err := checkJSONContentType(r); err != nil {
		return v, err
	}
	if r.Body == nil || r.Body == http.NoBody {
		return v, NewError(http.StatusBadRequest, "empty_body", "request body is required")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, decodeError(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return v, err
		}
		return v, NewError(http.StatusBadRequest, "invalid_json", "request body must contain a single JSON value")
	}
	if err := validatorFrom(r.Context()).Struct(r.Context(), &v); err != nil {
		return v, err
	}
	return v, nil
}

func checkJSONContentType(r *http.Request) error {
	ct := r.Header.Get("Content-Type")
	mt, _, err := mime.ParseMediaType(ct)
	if err == nil && (mt == "application/json" || (strings.HasPrefix(mt, "application/") && strings.HasSuffix(mt, "+json"))) {
		return nil
	}
	return NewError(http.StatusUnsupportedMediaType, "unsupported_media_type",
		"Content-Type must be application/json")
}

func decodeError(err error) error {
	var mbe *http.MaxBytesError
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &mbe):
		return err // rendered as 413 by the router
	case errors.Is(err, io.EOF):
		return NewError(http.StatusBadRequest, "empty_body", "request body is required")
	case errors.Is(err, io.ErrUnexpectedEOF):
		return &Error{Status: http.StatusBadRequest, Code: "invalid_json", Detail: "request body is truncated JSON", Err: err}
	case errors.As(err, &syn):
		return &Error{Status: http.StatusBadRequest, Code: "invalid_json",
			Detail: fmt.Sprintf("malformed JSON at offset %d", syn.Offset), Err: err}
	case errors.As(err, &typ):
		field := typ.Field
		if field == "" {
			return &Error{Status: http.StatusBadRequest, Code: "invalid_json",
				Detail: "request body must be a JSON " + typ.Type.Kind().String(), Err: err}
		}
		return &Error{Status: http.StatusBadRequest, Code: "invalid_json",
			Detail: "field " + field + " has the wrong type", Err: err,
			Fields: []FieldError{{Field: field, Code: "type", Message: "must be of type " + typ.Type.String()}}}
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		name := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
		return &Error{Status: http.StatusBadRequest, Code: "unknown_field",
			Detail: "unknown field " + name, Err: err,
			Fields: []FieldError{{Field: name, Code: "unknown_field", Message: "is not a known field"}}}
	}
	return &Error{Status: http.StatusBadRequest, Code: "invalid_json", Detail: "invalid JSON body", Err: err}
}

// JSON writes v as JSON with the given status. v is encoded before
// anything is written, so an encoding failure can still become a 500.
func JSON(w http.ResponseWriter, status int, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("web: encode response: %w", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err := w.Write(buf.Bytes())
	return err
}

// Created writes v with status 201 and a Location header.
func Created(w http.ResponseWriter, location string, v any) error {
	if location != "" {
		w.Header().Set("Location", location)
	}
	return JSON(w, http.StatusCreated, v)
}

// NoContent writes status 204.
func NoContent(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// Optional is a JSON field that distinguishes "absent" from "null". Use it
// in PATCH inputs for nullable fields: Set reports the key was present and
// Null that its value was null.
type Optional[T any] struct {
	Value T
	Set   bool
	Null  bool
}

// UnmarshalJSON implements json.Unmarshaler. It is only invoked when the
// key is present.
func (o *Optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(bytes.TrimSpace(b)) == "null" {
		o.Null = true
		var zero T
		o.Value = zero
		return nil
	}
	o.Null = false
	return json.Unmarshal(b, &o.Value)
}

// MarshalJSON implements json.Marshaler; absent and null both encode as null.
func (o Optional[T]) MarshalJSON() ([]byte, error) {
	if !o.Set || o.Null {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

// Ptr returns nil when o is absent or null, otherwise a pointer to Value.
func (o Optional[T]) Ptr() *T {
	if !o.Set || o.Null {
		return nil
	}
	v := o.Value
	return &v
}
