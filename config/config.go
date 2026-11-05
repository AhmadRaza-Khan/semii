// Package config loads a typed configuration struct from defaults, .env
// files and the process environment, failing fast with one error that lists
// every problem.
//
//	type Config struct {
//	    Env         string        `env:"SEMII_ENV" default:"development" validate:"oneof=development test production"`
//	    DatabaseURL config.Secret `env:"DATABASE_URL" validate:"required"`
//	    HTTP        HTTPConfig    `envPrefix:"HTTP_"`
//	}
//	cfg, err := config.Load[Config]()
//
// Sources, lowest to highest precedence: default tags, .env, .env.<SEMII_ENV>,
// .env.local (skipped when SEMII_ENV=test), the process environment. In
// production no .env file is read unless SEMII_DOTENV=1.
package config

import (
	"context"
	"encoding"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/AhmadRaza-Khan/semii/validate"
)

// Environments accepted for SEMII_ENV.
const (
	Development = "development"
	Test        = "test"
	Production  = "production"
)

// Option configures Load.
type Option func(*options)

type options struct {
	prefix string
	dir    string
	env    string
	lookup func(string) (string, bool)
}

// WithPrefix prepends prefix (e.g. "SHOP_") to every variable name except
// those starting with "SEMII_".
func WithPrefix(prefix string) Option { return func(o *options) { o.prefix = prefix } }

// WithDir sets the directory containing .env files (default ".").
func WithDir(dir string) Option { return func(o *options) { o.dir = dir } }

// WithEnv forces the environment instead of reading SEMII_ENV.
func WithEnv(name string) Option { return func(o *options) { o.env = name } }

// WithLookup replaces os.LookupEnv as the process-environment source,
// typically in tests.
func WithLookup(fn func(string) (string, bool)) Option {
	return func(o *options) { o.lookup = fn }
}

// Problem is one configuration error. Name is the environment variable
// (or "file:line" for .env syntax errors).
type Problem struct {
	Name    string
	Message string
}

// Error lists every problem found by Load.
type Error struct {
	Problems []Problem
}

func (e *Error) Error() string {
	var b strings.Builder
	n := len(e.Problems)
	fmt.Fprintf(&b, "config: %d error", n)
	if n != 1 {
		b.WriteByte('s')
	}
	for _, p := range e.Problems {
		fmt.Fprintf(&b, "\n  %s: %s", p.Name, p.Message)
	}
	return b.String()
}

// Load populates a T (a struct) from its env, default, envPrefix, sep and
// validate tags. On any problem it returns a *Error listing all of them.
//
// Supported field types: string, bool, ints, uints, floats, time.Duration,
// time.Time (RFC 3339), url.URL, netip.Prefix, netip.Addr, Secret, any
// encoding.TextUnmarshaler, pointers to these (nil when unset) and slices of
// these split on sep (default ","). An empty value counts as unset.
func Load[T any](opts ...Option) (T, error) {
	var cfg T
	o := options{dir: ".", lookup: os.LookupEnv}
	for _, opt := range opts {
		opt(&o)
	}
	rv := reflect.ValueOf(&cfg).Elem()
	if rv.Kind() != reflect.Struct {
		return cfg, fmt.Errorf("config: Load target %T must be a struct", cfg)
	}

	l := &loader{opts: o, paths: map[string]field{}}
	l.loadSources()
	l.walk(rv, "", "")
	l.validate(&cfg)
	if len(l.problems) > 0 {
		return cfg, &Error{Problems: l.problems}
	}
	return cfg, nil
}

type field struct {
	name   string // full environment variable name
	raw    string // value used, "" if unset
	secret bool
}

type loader struct {
	opts     options
	env      string
	files    map[string]string // merged .env values
	paths    map[string]field  // validator path -> field
	problems []Problem
}

func (l *loader) problem(name, msg string) {
	l.problems = append(l.problems, Problem{name, msg})
}

// loadSources resolves SEMII_ENV and reads the applicable .env files.
func (l *loader) loadSources() {
	l.files = map[string]string{}
	var base map[string]string
	read := func(name string) map[string]string {
		path := filepath.Join(l.opts.dir, name)
		f, err := os.Open(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				l.problem(path, err.Error())
			}
			return nil
		}
		defer f.Close()
		vals, problems := parseDotenv(f, path)
		l.problems = append(l.problems, problems...)
		return vals
	}

	env := l.opts.env
	if env == "" {
		env, _ = l.opts.lookup("SEMII_ENV")
	}
	if env == "" {
		// SEMII_ENV may itself come from .env; that file is the only one read
		// before the environment is known.
		base = read(".env")
		env = base["SEMII_ENV"]
	}
	if env == "" {
		env = Development
	}
	l.env = env

	if env == Production {
		if v, _ := l.opts.lookup("SEMII_DOTENV"); v != "1" {
			return // base, if read, is discarded: production ignores .env
		}
	}
	if base == nil {
		base = read(".env")
	}
	layers := []map[string]string{base, read(".env." + env)}
	if env != Test {
		layers = append(layers, read(".env.local"))
	}
	for _, layer := range layers {
		for k, v := range layer {
			l.files[k] = v
		}
	}
}

// value resolves name: process environment, then .env files.
func (l *loader) value(name string) (string, bool) {
	if name == "SEMII_ENV" {
		return l.env, true
	}
	if v, ok := l.opts.lookup(name); ok && v != "" {
		return v, true
	}
	if v, ok := l.files[name]; ok && v != "" {
		return v, true
	}
	return "", false
}

var (
	durationType = reflect.TypeFor[time.Duration]()
	urlType      = reflect.TypeFor[url.URL]()
	secretType   = reflect.TypeFor[Secret]()
	textType     = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// walk fills struct rv. prefix is the env prefix so far; vpath is the
// validator path so far (see fieldName).
func (l *loader) walk(rv reflect.Value, prefix, vpath string) {
	rt := rv.Type()
	for i := range rt.NumField() {
		sf := rt.Field(i)
		if !sf.IsExported() || sf.Tag.Get("env") == "-" {
			continue
		}
		fv := rv.Field(i)
		path := fieldName(sf)
		if vpath != "" {
			path = vpath + "." + path
		}

		envName, hasEnv := sf.Tag.Lookup("env")
		if p, ok := sf.Tag.Lookup("envPrefix"); ok || (!hasEnv && isNested(sf.Type)) {
			l.walk(fv, prefix+p, path)
			continue
		}
		if !hasEnv || envName == "" {
			l.problem(rt.Name()+"."+sf.Name, "missing env tag")
			continue
		}

		name := prefix + envName
		if !strings.HasPrefix(name, "SEMII_") {
			name = l.opts.prefix + name
		}
		f := field{name: name, secret: isSecret(sf.Type)}
		raw, ok := l.value(name)
		if !ok {
			raw, ok = sf.Tag.Lookup("default")
		}
		l.paths[path] = f
		if !ok || raw == "" {
			continue
		}
		f.raw = raw
		l.paths[path] = f
		sep := sf.Tag.Get("sep")
		if sep == "" {
			sep = ","
		}
		if err := setValue(fv, raw, sep); err != nil {
			msg := err.Error()
			if !f.secret {
				msg += fmt.Sprintf(" %q", raw)
			}
			l.problem(name, msg)
		}
	}
}

// fieldName names fields for validator paths: envPrefix, then env, then
// the Go name. It only has to be unique and consistent with walk.
func fieldName(sf reflect.StructField) string {
	if p, ok := sf.Tag.Lookup("envPrefix"); ok {
		return p
	}
	if e := sf.Tag.Get("env"); e != "" && e != "-" {
		return e
	}
	return sf.Name
}

func (l *loader) validate(cfg any) {
	v := validate.New(validate.WithFieldName(fieldName))
	err := v.Struct(context.Background(), cfg)
	var verrs validate.Errors
	if !errors.As(err, &verrs) {
		if err != nil {
			l.problem("config", err.Error())
		}
		return
	}
	for _, fe := range verrs {
		path := fe.Field
		if i := strings.IndexByte(path, '['); i >= 0 {
			path = path[:i]
		}
		f, ok := l.paths[path]
		if !ok {
			f.name = fe.Field
		}
		if l.reported(f.name) {
			continue // already failed to parse; the rule result is meaningless
		}
		msg := fe.Message
		if fe.Code == "required" {
			msg = "required"
		} else if f.raw != "" && !f.secret {
			msg += fmt.Sprintf(", got %q", f.raw)
		}
		l.problem(f.name, msg)
	}
}

func (l *loader) reported(name string) bool {
	for _, p := range l.problems {
		if p.Name == name {
			return true
		}
	}
	return false
}

// isNested reports whether t is a struct to recurse into rather than a
// leaf value.
func isNested(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t != urlType && !reflect.PointerTo(t).Implements(textType)
}

func isSecret(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	return t == secretType
}

func setValue(fv reflect.Value, raw, sep string) error {
	t := fv.Type()
	switch {
	case t.Kind() == reflect.Pointer:
		pv := reflect.New(t.Elem())
		if err := setValue(pv.Elem(), raw, sep); err != nil {
			return err
		}
		fv.Set(pv)
		return nil
	case t == urlType:
		u, err := url.Parse(raw)
		if err != nil {
			return errors.New("invalid URL")
		}
		fv.Set(reflect.ValueOf(*u))
		return nil
	case reflect.PointerTo(t).Implements(textType):
		if err := fv.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(raw)); err != nil {
			return fmt.Errorf("invalid %s", t)
		}
		return nil
	case t == durationType:
		d, err := time.ParseDuration(raw)
		if err != nil {
			return errors.New("invalid duration")
		}
		fv.SetInt(int64(d))
		return nil
	case t.Kind() == reflect.Slice:
		out := reflect.MakeSlice(t, 0, strings.Count(raw, sep)+1)
		for part := range strings.SplitSeq(raw, sep) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			ev := reflect.New(t.Elem()).Elem()
			if err := setValue(ev, part, sep); err != nil {
				return err
			}
			out = reflect.Append(out, ev)
		}
		fv.Set(out)
		return nil
	}

	switch t.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return errors.New("invalid bool")
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, t.Bits())
		if err != nil {
			return errors.New("invalid integer")
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, t.Bits())
		if err != nil {
			return errors.New("invalid unsigned integer")
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, t.Bits())
		if err != nil {
			return errors.New("invalid number")
		}
		fv.SetFloat(f)
	default:
		return fmt.Errorf("unsupported type %s", t)
	}
	return nil
}
