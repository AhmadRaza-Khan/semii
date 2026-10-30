package web

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// Logger logs one line per completed request with request_id, method,
// route (the matched pattern, not the raw path), status, duration_ms,
// bytes_out, client_ip and principal_id. 5xx responses log at Error, 4xx at
// Warn, others at Info. Requests for /healthz and /readyz are not logged.
func Logger(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}
			s, r := withState(r)
			rw := wrapWriter(w)
			start := time.Now()
			next.ServeHTTP(rw, r)

			status := rw.status
			if status == 0 {
				status = http.StatusOK // net/http's implicit status
			}
			level := slog.LevelInfo
			switch {
			case status >= 500:
				level = slog.LevelError
			case status >= 400:
				level = slog.LevelWarn
			}
			log.LogAttrs(r.Context(), level, "request",
				slog.String("request_id", s.requestID),
				slog.String("method", r.Method),
				slog.String("route", s.pattern),
				slog.Int("status", status),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
				slog.Int64("bytes_out", rw.bytes),
				slog.String("client_ip", ClientIP(r).String()),
				slog.String("principal_id", s.principalID),
			)
		})
	}
}

// Recover turns panics into 500 problem responses (when nothing was written
// yet) and logs them with a stack trace. http.ErrAbortHandler is re-panicked
// so net/http can abort the connection as intended.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rw := wrapWriter(w)
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				log.LogAttrs(r.Context(), slog.LevelError, "panic recovered",
					slog.String("request_id", RequestIDFrom(r.Context())),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())),
				)
				if !rw.written {
					WriteProblem(rw, r, NewError(http.StatusInternalServerError, "internal_error", "internal server error"))
				}
			}()
			next.ServeHTTP(rw, r)
		})
	}
}
