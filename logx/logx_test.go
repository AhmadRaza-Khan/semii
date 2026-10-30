package logx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

type secret string

func (secret) LogValue() slog.Value { return slog.StringValue("[redacted]") }

func TestNewWriterFormats(t *testing.T) {
	var buf bytes.Buffer
	NewWriter(&buf, "production", "warn").Info("hidden")
	NewWriter(&buf, "production", "warn").Warn("shown", "k", 1)
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil || m["msg"] != "shown" || m["level"] != "WARN" {
		t.Fatalf("production: %s", buf.String())
	}

	buf.Reset()
	NewWriter(&buf, "test", "debug").Debug("dbg")
	if !strings.HasPrefix(buf.String(), "{") {
		t.Errorf("test env not JSON: %s", buf.String())
	}

	buf.Reset()
	NewWriter(&buf, "development", "info").Info("hello", "user", "bob")
	if line := buf.String(); !strings.Contains(line, " INF hello user=bob\n") || strings.Contains(line, "\x1b[") {
		t.Errorf("development: %q", line)
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug, "INFO": slog.LevelInfo, "warn": slog.LevelWarn,
		"warning": slog.LevelWarn, "error": slog.LevelError, "bogus": slog.LevelInfo, "": slog.LevelInfo,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("%q: %v", in, got)
		}
	}
}

func TestDevHandler(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewDevHandler(&buf, slog.LevelDebug, false)).
		With("app", "shop").WithGroup("req").With("id", 7)
	log.Debug("msg",
		"s", "two words", "q", `say "hi"`, "eq", "a=b", "empty", "",
		"d", 1500*time.Millisecond, "t", time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		"err", errors.New("boom"), "sec", secret("hunter2"),
		slog.Group("g", "x", 1), slog.Attr{}, "b", true,
	)
	line := buf.String()
	for _, want := range []string{
		" DBG msg app=shop req.id=7 ", `req.s="two words"`, `req.q="say \"hi\""`, `req.eq="a=b"`, `req.empty=""`,
		"req.d=1.5s", "req.t=2024-01-02T03:04:05Z", "req.err=boom", "req.sec=[redacted]", "req.g.x=1", "req.b=true",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %q", want, line)
		}
	}
	if strings.Contains(line, "hunter2") {
		t.Error("LogValuer not honoured")
	}

	buf.Reset()
	h := NewDevHandler(&buf, nil, true)
	if h.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("nil level should default to info")
	}
	if h.WithGroup("") != h {
		t.Error("empty group should be a no-op")
	}
	l := slog.New(h)
	l.Info("i")
	l.Warn("w")
	l.Error("e", "k", "v")
	out := buf.String()
	for _, want := range []string{ansiBlue + "INF", ansiYellow + "WRN", ansiRed + "ERR", ansiCyan + "k=" + ansiReset} {
		if !strings.Contains(out, want) {
			t.Errorf("missing color %q in %q", want, out)
		}
	}
	// Records without a time omit it.
	buf.Reset()
	_ = h.Handle(context.Background(), slog.NewRecord(time.Time{}, slog.LevelInfo, "notime", 0))
	if !strings.HasPrefix(buf.String(), ansiBlue+"INF") {
		t.Errorf("%q", buf.String())
	}
}

func TestContext(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, nil)).With("request_id", "r1")
	ctx := NewContext(context.Background(), l)
	FromContext(ctx).Info("x")
	if !strings.Contains(buf.String(), `"request_id":"r1"`) {
		t.Errorf("%s", buf.String())
	}
	if FromContext(context.Background()) != slog.Default() {
		t.Error("fallback is not slog.Default")
	}
}

func TestIsTerminal(t *testing.T) {
	if isTerminal(&bytes.Buffer{}) {
		t.Error("buffer is not a terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("regular file is not a terminal")
	}
	if New("production", "info") == nil {
		t.Error("nil logger")
	}
}
