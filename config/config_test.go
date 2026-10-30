package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/semii/semii/web"
)

type httpConfig struct {
	Addr           string         `env:"ADDR" default:":8080" validate:"hostname_port"`
	MaxBodyBytes   int64          `env:"MAX_BODY_BYTES" default:"1048576"`
	HandlerTimeout time.Duration  `env:"HANDLER_TIMEOUT" default:"30s"`
	TrustedProxies []netip.Prefix `env:"TRUSTED_PROXIES"`
	CORS           web.CORSConfig `envPrefix:"CORS_"`
}

type appConfig struct {
	Env         string     `env:"SEMII_ENV" default:"development" validate:"oneof=development test production"`
	LogLevel    string     `env:"LOG_LEVEL" default:"info" validate:"oneof=debug info warn error"`
	DatabaseURL Secret     `env:"DATABASE_URL" validate:"required"`
	HTTP        httpConfig `envPrefix:"HTTP_"`
}

func env(kv ...string) Option {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return WithLookup(func(k string) (string, bool) { v, ok := m[k]; return v, ok })
}

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func problems(t *testing.T, err error) map[string]string {
	t.Helper()
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("want *Error, got %v", err)
	}
	out := map[string]string{}
	for _, p := range ce.Problems {
		out[p.Name] = p.Message
	}
	return out
}

func TestLoadDefaultsAndEnv(t *testing.T) {
	cfg, err := Load[appConfig](WithDir(t.TempDir()), env(
		"DATABASE_URL", "postgres://u:p@h/db",
		"HTTP_TRUSTED_PROXIES", "10.0.0.0/8, 127.0.0.1/32",
		"HTTP_CORS_ALLOWED_ORIGINS", "https://a.com,https://b.com",
		"HTTP_CORS_MAX_AGE", "1h",
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env != Development || cfg.LogLevel != "info" || cfg.DatabaseURL.Reveal() != "postgres://u:p@h/db" ||
		cfg.HTTP.Addr != ":8080" || cfg.HTTP.MaxBodyBytes != 1<<20 || cfg.HTTP.HandlerTimeout != 30*time.Second ||
		len(cfg.HTTP.TrustedProxies) != 2 || cfg.HTTP.TrustedProxies[1].String() != "127.0.0.1/32" ||
		len(cfg.HTTP.CORS.AllowedOrigins) != 2 || cfg.HTTP.CORS.MaxAge != time.Hour {
		t.Errorf("%+v", cfg)
	}
}

func TestAllErrorsReported(t *testing.T) {
	_, err := Load[appConfig](WithDir(t.TempDir()), env(
		"HTTP_ADDR", "8080",
		"HTTP_MAX_BODY_BYTES", "lots",
		"LOG_LEVEL", "loud",
	))
	got := problems(t, err)
	want := map[string]string{
		"DATABASE_URL":        "required",
		"HTTP_ADDR":           `must be host:port, got "8080"`,
		"HTTP_MAX_BODY_BYTES": `invalid integer "lots"`,
		"LOG_LEVEL":           `must be one of: debug, info, warn, error, got "loud"`,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	if msg := err.Error(); !strings.HasPrefix(msg, "config: 4 errors\n  ") || !strings.Contains(msg, "\n  DATABASE_URL: required") {
		t.Errorf("%q", msg)
	}
	one := &Error{Problems: []Problem{{"A", "required"}}}
	if one.Error() != "config: 1 error\n  A: required" {
		t.Errorf("%q", one.Error())
	}
}

func TestPrecedence(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		".env":             "A=env\nB=env\nC=env\nD=env\nE=env\n",
		".env.development": "B=dev\nC=dev\nD=dev\n",
		".env.local":       "C=local\nD=local\n",
		".env.test":        "B=test\n",
	})
	type tagged struct {
		A string `env:"A"`
		B string `env:"B"`
		C string `env:"C"`
		D string `env:"D"`
		E string `env:"E" default:"default"`
		F string `env:"F" default:"default"`
	}
	got, err := Load[tagged](WithDir(dir), env("D", "process", "E", ""))
	if err != nil {
		t.Fatal(err)
	}
	want := tagged{A: "env", B: "dev", C: "local", D: "process", E: "env", F: "default"}
	if got != want {
		t.Errorf("development: %+v", got)
	}

	// test skips .env.local and uses .env.test.
	got, _ = Load[tagged](WithDir(dir), WithEnv(Test), env())
	if got.B != "test" || got.C != "env" {
		t.Errorf("test: %+v", got)
	}

	// production ignores .env files unless SEMII_DOTENV=1.
	got, _ = Load[tagged](WithDir(dir), env("SEMII_ENV", Production))
	if got.A != "" || got.F != "default" {
		t.Errorf("production: %+v", got)
	}
	got, _ = Load[tagged](WithDir(dir), env("SEMII_ENV", Production, "SEMII_DOTENV", "1"))
	if got.A != "env" || got.C != "local" {
		t.Errorf("production+dotenv: %+v", got)
	}
}

func TestEnvFromDotenvAndPrefix(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		".env":      "SEMII_ENV=test\nSHOP_NAME=fromenv\n",
		".env.test": "SHOP_NAME=fromtest\n",
	})
	type cfg struct {
		Env  string `env:"SEMII_ENV"`
		Name string `env:"NAME"`
	}
	got, err := Load[cfg](WithDir(dir), WithPrefix("SHOP_"), env())
	if err != nil {
		t.Fatal(err)
	}
	if got.Env != Test || got.Name != "fromtest" {
		t.Errorf("%+v", got)
	}
	// WithEnv wins over everything and is visible in the struct.
	got, _ = Load[cfg](WithDir(dir), WithPrefix("SHOP_"), WithEnv(Production), env("SEMII_ENV", Test))
	if got.Env != Production || got.Name != "" {
		t.Errorf("%+v", got)
	}
}

type level int

func (l *level) UnmarshalText(b []byte) error {
	switch string(b) {
	case "low":
		*l = 1
	case "high":
		*l = 2
	default:
		return errors.New("bad level")
	}
	return nil
}

func TestTypes(t *testing.T) {
	type nested struct {
		Flag bool `env:"FLAG"`
	}
	type cfg struct {
		S      string        `env:"S"`
		B      bool          `env:"B"`
		I8     int8          `env:"I8"`
		U      uint16        `env:"U"`
		F      float64       `env:"F"`
		D      time.Duration `env:"D"`
		T      time.Time     `env:"T"`
		URL    url.URL       `env:"URL"`
		URLP   *url.URL      `env:"URLP"`
		Addr   netip.Addr    `env:"ADDR"`
		ID     uuid.UUID     `env:"ID"`
		Lvl    level         `env:"LVL"`
		Ints   []int         `env:"INTS" sep:";"`
		Opt    *int          `env:"OPT"`
		Unset  *int          `env:"UNSET"`
		Secret Secret        `env:"SECRET"`
		Nested nested        // no envPrefix: walked without prefix
		Skip   string        `env:"-"`
		hidden string
	}
	id := uuid.New()
	got, err := Load[cfg](WithDir(t.TempDir()), env(
		"S", "str", "B", "true", "I8", "-5", "U", "65535", "F", "1.5", "D", "90s",
		"T", "2024-01-02T03:04:05Z", "URL", "https://x.com/p?q=1", "URLP", "http://y",
		"ADDR", "::1", "ID", id.String(), "LVL", "high", "INTS", "1; 2;;3", "OPT", "7",
		"SECRET", "hunter2", "FLAG", "1", "Skip", "x",
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.S != "str" || !got.B || got.I8 != -5 || got.U != 65535 || got.F != 1.5 || got.D != 90*time.Second ||
		got.T.Year() != 2024 || got.URL.Host != "x.com" || got.URLP.Host != "y" || got.Addr.String() != "::1" ||
		got.ID != id || got.Lvl != 2 || fmt.Sprint(got.Ints) != "[1 2 3]" || *got.Opt != 7 || got.Unset != nil ||
		got.Secret.Reveal() != "hunter2" || !got.Nested.Flag || got.Skip != "" || got.hidden != "" {
		t.Errorf("%+v", got)
	}

	_, err = Load[cfg](WithDir(t.TempDir()), env(
		"B", "maybe", "I8", "300", "U", "-1", "F", "x", "D", "5", "T", "yesterday", "URL", "http://[::1",
		"ADDR", "nope", "LVL", "mid", "INTS", "1;x", "OPT", "o",
	))
	got2 := problems(t, err)
	for _, name := range []string{"B", "I8", "U", "F", "D", "T", "URL", "ADDR", "LVL", "INTS", "OPT"} {
		if _, ok := got2[name]; !ok {
			t.Errorf("no problem for %s: %v", name, got2)
		}
	}
}

func TestSecretNeverPrinted(t *testing.T) {
	type cfg struct {
		Key Secret `env:"KEY" validate:"min=10"`
		N   Secret `env:"N"`
	}
	_, err := Load[cfg](WithDir(t.TempDir()), env("KEY", "hunter2"))
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("secret leaked in validation error: %v", err)
	}

	s := Secret("hunter2")
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "s", s)
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "s", s)
	j, _ := json.Marshal(struct{ S Secret }{s})
	txt, _ := s.MarshalText()
	out := fmt.Sprintf("%v %s %+v %#v %q %x", s, s, s, s, s, s) + buf.String() + string(j) + string(txt)
	if strings.Contains(out, "hunter2") || !strings.Contains(out, redacted) {
		t.Errorf("leaked: %s", out)
	}
}

func TestMisuse(t *testing.T) {
	if _, err := Load[int](); err == nil {
		t.Error("non-struct accepted")
	}
	type noTag struct{ Name string }
	_, err := Load[noTag](WithDir(t.TempDir()), env())
	if p := problems(t, err); p["noTag.Name"] != "missing env tag" {
		t.Errorf("%v", p)
	}
}

func TestDotenvSyntax(t *testing.T) {
	src := strings.Join([]string{
		"# comment",
		"",
		"PLAIN=value # trailing comment",
		"export EXPORTED=yes",
		"  SPACED = padded  ",
		"HASH=a#b",
		"EMPTY=",
		`DQ="line1\nline2 \"q\" \\ \x"`,
		`SQ='literal \n $HOME'`,
		"NOEXPAND=$PLAIN",
		`MULTI="-----BEGIN`,
		`body`,
		`-----END" # pem`,
		"bad line",
		"1BAD=x",
		`TRAIL="x" y`,
		`UNTERM='x`,
	}, "\n")
	vals, probs := parseDotenv(strings.NewReader(src), ".env")
	want := map[string]string{
		"PLAIN": "value", "EXPORTED": "yes", "SPACED": "padded", "HASH": "a#b", "EMPTY": "",
		"DQ": "line1\nline2 \"q\" \\ \\x", "SQ": `literal \n $HOME`, "NOEXPAND": "$PLAIN",
		"MULTI": "-----BEGIN\nbody\n-----END",
	}
	if fmt.Sprint(vals) != fmt.Sprint(want) {
		t.Errorf("got  %q\nwant %q", vals, want)
	}
	wantProbs := []string{".env:14", ".env:15", ".env:16", ".env:17"}
	if len(probs) != len(wantProbs) {
		t.Fatalf("problems %v", probs)
	}
	for i, p := range probs {
		if p.Name != wantProbs[i] {
			t.Errorf("problem %d at %s, want %s (%s)", i, p.Name, wantProbs[i], p.Message)
		}
	}

	_, probs = parseDotenv(strings.NewReader(`X="never closed`+"\nmore"), ".env")
	if len(probs) != 1 || probs[0].Message != "unterminated quoted value" {
		t.Errorf("%v", probs)
	}
}

func TestDotenvErrorsSurface(t *testing.T) {
	dir := writeFiles(t, map[string]string{".env": "oops\n"})
	type cfg struct {
		A string `env:"A"`
	}
	_, err := Load[cfg](WithDir(dir), env())
	if p := problems(t, err); p[filepath.Join(dir, ".env")+":1"] == "" {
		t.Errorf("%v", p)
	}
	// An unreadable path (a directory named .env.local) is reported too.
	dir = t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".env.local"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load[cfg](WithDir(dir), env()); err == nil {
		t.Error("want error for unreadable .env.local")
	}
}

func TestParseFailureNotDoubleReported(t *testing.T) {
	type cfg struct {
		N int `env:"N" validate:"required,min=5"`
	}
	_, err := Load[cfg](WithDir(t.TempDir()), env("N", "abc"))
	if p := problems(t, err); len(p) != 1 || p["N"] != `invalid integer "abc"` {
		t.Errorf("%v", p)
	}
}
