// Package logx builds the application's *slog.Logger and carries a
// request-scoped logger through context.Context.
//
// There is no package-level logger: build one in main with New and pass it
// down explicitly.
package logx

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// New returns a logger for env writing to stdout. Development uses a
// compact, colored text format (colors only on a terminal and when NO_COLOR
// is unset); test and production use JSON. level is one of debug, info,
// warn, error (anything else means info).
func New(env, level string) *slog.Logger {
	return NewWriter(os.Stdout, env, level)
}

// NewWriter is New writing to w. Colors are enabled only when w is a
// terminal and NO_COLOR is unset.
func NewWriter(w io.Writer, env, level string) *slog.Logger {
	lvl := ParseLevel(level)
	if env == "development" || env == "" {
		_, noColor := os.LookupEnv("NO_COLOR")
		return slog.New(NewDevHandler(w, lvl, !noColor && isTerminal(w)))
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl}))
}

// ParseLevel parses debug, info, warn or error (case-insensitive);
// anything else yields slog.LevelInfo.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

type ctxKey struct{}

// NewContext returns ctx carrying log. web.Logger uses it to store a
// logger enriched with the request ID.
func NewContext(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, log)
}

// FromContext returns the logger stored by NewContext, or slog.Default()
// when there is none.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
