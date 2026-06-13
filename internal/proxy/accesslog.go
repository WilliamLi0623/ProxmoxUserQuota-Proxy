package proxy

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// WithAccessLog wraps a handler with a structured access log. In P2 this is
// where identity parsing and request classification will hook in.
func WithAccessLog(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		logger.Info("req",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"dur_ms", time.Since(start).Milliseconds(),
			"bytes_out", sw.bytes,
			"remote", r.RemoteAddr,
			"upgrade", r.Header.Get("Upgrade"),
		)
	})
}

// statusWriter records status/bytes while staying hijackable: websocket
// upgrades (noVNC/xterm.js/SPICE) need the underlying connection. Breaking
// hijackability here is the classic way to break consoles behind a proxy.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the real ResponseWriter.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Hijack keeps protocol upgrades working through the wrapper.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// Flush forwards streaming flushes.
func (w *statusWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
