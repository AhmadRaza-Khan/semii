package web

import (
	"bufio"
	"net"
	"net/http"
)

// responseWriter records the status and byte count of a response. It keeps
// http.Flusher and http.Hijacker available and implements Unwrap so
// http.ResponseController can reach the underlying writer.
type responseWriter struct {
	http.ResponseWriter
	status  int
	bytes   int64
	written bool
}

// writeTracker is implemented by semii's ResponseWriter wrappers so the
// router can tell whether a handler wrote before returning an error.
type writeTracker interface {
	wroteHeader() bool
}

func (w *responseWriter) wroteHeader() bool { return w.written }

// wrapWriter returns w as a *responseWriter, wrapping it only if needed.
func wrapWriter(w http.ResponseWriter) *responseWriter {
	if rw, ok := w.(*responseWriter); ok {
		return rw
	}
	return &responseWriter{ResponseWriter: w}
}

func (w *responseWriter) WriteHeader(code int) {
	if !w.written {
		w.status = code
		// 1xx responses are informational; the final header is still to come.
		w.written = code >= 200
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.status, w.written = http.StatusOK, true
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Status returns the response status, or 0 if nothing was written.
func (w *responseWriter) Status() int { return w.status }

func (w *responseWriter) Flush() {
	if !w.written {
		w.status, w.written = http.StatusOK, true
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.written = true
	}
	return conn, rw, err
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
