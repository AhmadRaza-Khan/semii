// Package validate validates struct shapes using struct tags and reports
// failures as a flat list of field errors keyed by their JSON (or query)
// names.
//
// It wraps github.com/go-playground/validator/v10 behind a small API so the
// engine can be swapped without touching user code.
package validate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// FieldError describes one failed rule on one field.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Param   string `json:"param,omitempty"`
}

// Errors is the error returned by Validator.Struct when validation fails.
type Errors []FieldError

// Error implements error.
func (e Errors) Error() string {
	if len(e) == 0 {
		return "validation failed"
	}
	parts := make([]string, len(e))
	for i, fe := range e {
		parts[i] = fe.Field + ": " + fe.Message
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// Func is a custom rule. value is the field value (pointers already
// dereferenced) and param is the text after "=" in the tag, if any.
type Func func(ctx context.Context, value any, param string) bool

// Option configures a Validator.
type Option func(*options)

type options struct {
	passwordMin, passwordMax int
}

// WithPasswordLength overrides the length bounds of the built-in "password"
// rule (default 8..128 characters).
func WithPasswordLength(min, max int) Option {
	return func(o *options) { o.passwordMin, o.passwordMax = min, max }
}

// Validator validates structs. It is safe for concurrent use. Register must
// only be called during setup, before the Validator is shared.
type Validator struct {
	v        *validator.Validate
	messages map[string]string
}

// Default is the Validator used when none is configured. It is the only
// package-level value in semii and must be treated as read-only: register
// custom rules on a Validator created with New instead.
var Default = New()

// New returns a Validator with semii's built-in rules registered.
func New(opts ...Option) *Validator {
	o := options{passwordMin: 8, passwordMax: 128}
	for _, opt := range opts {
		opt(&o)
	}
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(fieldName)
	val := &Validator{v: v, messages: map[string]string{}}
	for _, r := range builtinRules(o) {
		if err := val.Register(r.tag, r.fn, r.msg); err != nil {
			panic(err) // built-in rules are static; failure is a programming error
		}
	}
	return val
}

// Register adds a custom rule under tag. message is used for failures and
// may contain "{param}", which is replaced by the tag parameter.
func (v *Validator) Register(tag string, fn Func, message string) error {
	if tag == "" || fn == nil {
		return errors.New("validate: tag and fn are required")
	}
	err := v.v.RegisterValidationCtx(tag, func(ctx context.Context, fl validator.FieldLevel) bool {
		return fn(ctx, fl.Field().Interface(), fl.Param())
	})
	if err != nil {
		return fmt.Errorf("validate: register %q: %w", tag, err)
	}
	v.messages[tag] = message
	return nil
}

// Struct validates s, which must be a struct or a pointer to one. It returns
// nil, an Errors value, or a non-validation error for misuse. Values that are
// not structs (slices, maps, scalars) are not validated and return nil.
func (v *Validator) Struct(ctx context.Context, s any) error {
	rv := reflect.ValueOf(s)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil
	}
	err := v.v.StructCtx(ctx, s)
	if err == nil {
		return nil
	}
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return fmt.Errorf("validate: %w", err)
	}
	out := make(Errors, 0, len(verrs))
	for _, fe := range verrs {
		out = append(out, v.convert(rv.Type(), fe))
	}
	return out
}

func (v *Validator) convert(root reflect.Type, fe validator.FieldError) FieldError {
	// Namespace is "Root.field.sub[2].x" using tag names; drop the root.
	field := fe.Namespace()
	if i := strings.IndexByte(field, '.'); i >= 0 {
		field = field[i+1:]
	}
	out := FieldError{Field: field, Code: fe.Tag(), Param: fe.Param()}
	if msg, ok := customMessage(root, fe.StructNamespace()); ok {
		out.Message = msg
	} else {
		out.Message = v.message(fe)
	}
	return out
}

// fieldName prefers the query tag, then json, then the Go name.
func fieldName(f reflect.StructField) string {
	for _, key := range []string{"query", "json"} {
		if tag, ok := f.Tag.Lookup(key); ok {
			name, _, _ := strings.Cut(tag, ",")
			if name == "-" {
				return ""
			}
			if name != "" {
				return name
			}
		}
	}
	return f.Name
}

// customMessage resolves a `msg:"..."` override by walking the struct
// namespace ("Root.Field.Items[2].Qty") through the type.
func customMessage(t reflect.Type, ns string) (string, bool) {
	segs := strings.Split(ns, ".")
	if len(segs) < 2 {
		return "", false
	}
	var sf reflect.StructField
	for _, seg := range segs[1:] {
		if i := strings.IndexByte(seg, '['); i >= 0 {
			seg = seg[:i]
		}
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return "", false
		}
		f, ok := t.FieldByName(seg)
		if !ok {
			return "", false
		}
		sf = f
		t = f.Type
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
			t = t.Elem()
		}
	}
	msg, ok := sf.Tag.Lookup("msg")
	return msg, ok && msg != ""
}
