package web

import (
	"bufio"
	"compress/gzip"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const gzipMinSize = 1024

// Gzip compresses responses when the client accepts gzip, the body is at
// least 1 KiB, the content type is JSON or text (except text/event-stream)
// and no Content-Encoding is already set. The first KiB is buffered to make
// that decision; Flush forces it early.
func Gzip() Middleware {
	pool := &sync.Pool{New: func() any {
		gz, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression)
		return gz
	}}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
				next.ServeHTTP(w, r)
				return
			}
			gw := &gzipWriter{ResponseWriter: w, pool: pool, head: r.Method == http.MethodHead}
			defer gw.close()
			next.ServeHTTP(gw, r)
		})
	}
}

func acceptsGzip(ae string) bool {
	for part := range strings.SplitSeq(ae, ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		if q, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if f, err := strconv.ParseFloat(q, 64); err == nil && f == 0 {
				return false
			}
		}
		return true
	}
	return false
}

type gzipWriter struct {
	http.ResponseWriter
	pool    *sync.Pool
	gz      *gzip.Writer
	buf     []byte
	status  int
	head    bool
	decided bool
}

// wroteHeader reports whether the handler committed a status or body, even
// if it is still buffered here.
func (w *gzipWriter) wroteHeader() bool { return w.status != 0 || len(w.buf) > 0 || w.decided }

func (w *gzipWriter) WriteHeader(code int) {
	if w.decided || w.status != 0 {
		if w.decided {
			w.ResponseWriter.WriteHeader(code)
		}
		return
	}
	if code < 200 {
		w.ResponseWriter.WriteHeader(code) // informational, pass through
		return
	}
	w.status = code
	if code == http.StatusNoContent || code == http.StatusNotModified || w.head {
		w.decide(false)
	}
}

func (w *gzipWriter) Write(b []byte) (int, error) {
	if !w.decided {
		w.buf = append(w.buf, b...)
		if len(w.buf) < gzipMinSize {
			return len(b), nil
		}
		w.decide(w.eligible())
		if err := w.flushBuf(); err != nil {
			return 0, err
		}
		return len(b), nil
	}
	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *gzipWriter) eligible() bool {
	h := w.Header()
	if h.Get("Content-Encoding") != "" {
		return false
	}
	ct := h.Get("Content-Type")
	if ct == "" {
		if len(w.buf) == 0 {
			return false
		}
		ct = http.DetectContentType(w.buf)
		h.Set("Content-Type", ct)
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	switch {
	case mt == "text/event-stream":
		return false
	case strings.HasPrefix(mt, "text/"), mt == "application/json", strings.HasSuffix(mt, "+json"):
		return true
	}
	return false
}

// decide commits the response headers, compressed or not.
func (w *gzipWriter) decide(compress bool) {
	w.decided = true
	h := w.Header()
	if w.status != http.StatusNoContent && w.status != http.StatusNotModified {
		h.Add("Vary", "Accept-Encoding")
	}
	if compress {
		h.Del("Content-Length")
		h.Set("Content-Encoding", "gzip")
		w.gz = w.pool.Get().(*gzip.Writer)
		w.gz.Reset(w.ResponseWriter)
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(w.status)
}

func (w *gzipWriter) flushBuf() error {
	if len(w.buf) == 0 {
		return nil
	}
	var err error
	if w.gz != nil {
		_, err = w.gz.Write(w.buf)
	} else {
		_, err = w.ResponseWriter.Write(w.buf)
	}
	w.buf = nil
	return err
}

func (w *gzipWriter) close() {
	if !w.decided {
		if w.status == 0 && len(w.buf) == 0 {
			return // nothing written; let net/http send its default
		}
		w.decide(false)
		_ = w.flushBuf()
	}
	if w.gz != nil {
		_ = w.gz.Close()
		w.gz.Reset(nil)
		w.pool.Put(w.gz)
		w.gz = nil
	}
}

// Flush forces the compression decision and flushes buffered data.
func (w *gzipWriter) Flush() {
	if !w.decided {
		w.decide(w.eligible())
		_ = w.flushBuf()
	}
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *gzipWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *gzipWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
