package web

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"
)

var dateType = reflect.TypeFor[Date]()

// DateLayout is the wire format of Date.
const DateLayout = "2006-01-02"

// Date is a calendar date without time of day, encoded in JSON, query
// strings and config as "2006-01-02". It is stored at midnight UTC.
type Date struct {
	time.Time
}

// NewDate returns the date y-m-d.
func NewDate(y int, m time.Month, d int) Date {
	return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

// ParseDate parses "2006-01-02".
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return Date{}, errors.New("must be a date in YYYY-MM-DD format")
	}
	return Date{t}, nil
}

// String returns "2006-01-02".
func (d Date) String() string { return d.Format(DateLayout) }

// MarshalJSON implements json.Marshaler.
func (d Date) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// UnmarshalJSON implements json.Unmarshaler; null leaves d unchanged.
func (d *Date) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return &json.UnmarshalTypeError{Value: "non-string", Type: dateType}
	}
	parsed, err := ParseDate(s)
	if err != nil {
		// A type error lets encoding/json attach the field path.
		return &json.UnmarshalTypeError{Value: "string " + strconv.Quote(s), Type: dateType}
	}
	*d = parsed
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Date) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler (query strings, config).
func (d *Date) UnmarshalText(b []byte) error {
	parsed, err := ParseDate(string(b))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// DateFromPtr converts a nullable time to a nullable Date.
func DateFromPtr(t *time.Time) *Date {
	if t == nil {
		return nil
	}
	return &Date{*t}
}

// TimePtr converts a nullable Date to a nullable time. It is safe on nil.
func (d *Date) TimePtr() *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time
	return &t
}
