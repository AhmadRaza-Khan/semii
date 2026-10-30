package config

import "log/slog"

const redacted = "[redacted]"

// Secret is a string that never prints its value: String, GoString,
// MarshalJSON, MarshalText and LogValue all return "[redacted]". Call Reveal
// where the real value is needed.
type Secret string

// Reveal returns the secret value.
func (s Secret) Reveal() string { return string(s) }

// String implements fmt.Stringer.
func (s Secret) String() string { return redacted }

// GoString implements fmt.GoStringer, covering %#v.
func (s Secret) GoString() string { return `config.Secret("` + redacted + `")` }

// MarshalJSON implements json.Marshaler.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// MarshalText implements encoding.TextMarshaler.
func (s Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// LogValue implements slog.LogValuer.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }
