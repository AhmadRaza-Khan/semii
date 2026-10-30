package logx

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"
	"unicode"
)

const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"
	ansiCyan   = "\x1b[36m"
)

// DevHandler writes one human-readable line per record:
//
//	15:04:05.000 INF request method=GET route="GET /orders" status=200
//
// It is meant for local development; use slog.JSONHandler elsewhere.
type DevHandler struct {
	mu     *sync.Mutex
	w      io.Writer
	level  slog.Leveler
	color  bool
	attrs  []byte // pre-rendered attributes from WithAttrs
	prefix string // group prefix from WithGroup, e.g. "req."
}

// NewDevHandler returns a DevHandler writing records at or above level.
func NewDevHandler(w io.Writer, level slog.Leveler, color bool) *DevHandler {
	if level == nil {
		level = slog.LevelInfo
	}
	return &DevHandler{mu: &sync.Mutex{}, w: w, level: level, color: color}
}

// Enabled implements slog.Handler.
func (h *DevHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

// WithAttrs implements slog.Handler.
func (h *DevHandler) WithAttrs(as []slog.Attr) slog.Handler {
	h2 := *h
	h2.attrs = append([]byte(nil), h.attrs...)
	for _, a := range as {
		h2.attrs = h2.appendAttr(h2.attrs, h.prefix, a)
	}
	return &h2
}

// WithGroup implements slog.Handler.
func (h *DevHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	h2 := *h
	h2.prefix = h.prefix + name + "."
	return &h2
}

// Handle implements slog.Handler.
func (h *DevHandler) Handle(_ context.Context, r slog.Record) error {
	buf := make([]byte, 0, 256)
	if !r.Time.IsZero() {
		buf = h.paint(buf, ansiDim, r.Time.Format("15:04:05.000"))
		buf = append(buf, ' ')
	}
	buf = h.appendLevel(buf, r.Level)
	buf = append(buf, ' ')
	buf = append(buf, r.Message...)
	buf = append(buf, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		buf = h.appendAttr(buf, h.prefix, a)
		return true
	})
	buf = append(buf, '\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf)
	return err
}

func (h *DevHandler) appendLevel(buf []byte, l slog.Level) []byte {
	switch {
	case l >= slog.LevelError:
		return h.paint(buf, ansiRed, "ERR")
	case l >= slog.LevelWarn:
		return h.paint(buf, ansiYellow, "WRN")
	case l >= slog.LevelInfo:
		return h.paint(buf, ansiBlue, "INF")
	}
	return h.paint(buf, ansiDim, "DBG")
}

func (h *DevHandler) appendAttr(buf []byte, prefix string, a slog.Attr) []byte {
	a.Value = a.Value.Resolve() // honours slog.LogValuer, e.g. config.Secret
	if a.Equal(slog.Attr{}) {
		return buf
	}
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		if a.Key != "" {
			prefix += a.Key + "."
		}
		for _, ga := range group {
			buf = h.appendAttr(buf, prefix, ga)
		}
		return buf
	}
	buf = append(buf, ' ')
	buf = h.paint(buf, ansiCyan, prefix+a.Key+"=")
	return appendValue(buf, a.Value)
}

func appendValue(buf []byte, v slog.Value) []byte {
	switch v.Kind() {
	case slog.KindString:
		return appendString(buf, v.String())
	case slog.KindTime:
		return v.Time().AppendFormat(buf, time.RFC3339Nano)
	case slog.KindDuration:
		return append(buf, v.Duration().String()...)
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			return appendString(buf, err.Error())
		}
		return appendString(buf, v.String())
	}
	return append(buf, v.String()...)
}

func appendString(buf []byte, s string) []byte {
	if needsQuote(s) {
		return strconv.AppendQuote(buf, s)
	}
	return append(buf, s...)
}

func needsQuote(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if unicode.IsSpace(r) || r == '"' || r == '=' || !unicode.IsPrint(r) {
			return true
		}
	}
	return false
}

func (h *DevHandler) paint(buf []byte, color, s string) []byte {
	if !h.color {
		return append(buf, s...)
	}
	buf = append(buf, color...)
	buf = append(buf, s...)
	return append(buf, ansiReset...)
}

var _ slog.Handler = (*DevHandler)(nil)
