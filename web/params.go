package web

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
)

// PathUUID parses the path parameter name as a UUID. A malformed value
// returns a 400 invalid_path_param error.
func PathUUID(r *http.Request, name string) (uuid.UUID, error) {
	raw := r.PathValue(name)
	id, err := uuid.Parse(raw)
	if err != nil || len(raw) != 36 {
		return uuid.Nil, invalidPathParam(name, "must be a valid UUID")
	}
	return id, nil
}

// PathInt64 parses the path parameter name as a base-10 int64. A malformed
// value returns a 400 invalid_path_param error.
func PathInt64(r *http.Request, name string) (int64, error) {
	n, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		return 0, invalidPathParam(name, "must be an integer")
	}
	return n, nil
}

func invalidPathParam(name, msg string) *Error {
	e := NewError(http.StatusBadRequest, "invalid_path_param", "path parameter "+name+" "+msg)
	e.Fields = []FieldError{{Field: name, Code: "invalid_path_param", Message: msg}}
	return e
}
