package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/raythx98/go-travel/internal/tracing"
)

// responseWriter wraps http.ResponseWriter to capture the status code.
// It delegates Flush to the underlying writer if it implements http.Flusher,
// which is required for SSE streams to work through the middleware stack.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Logging attaches a trace ID to each request and logs method, path, status, and latency.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		traceID := r.Header.Get("X-Trace-Id")
		if traceID == "" {
			traceID = tracing.NewID()
		}

		ctx := context.WithValue(r.Context(), tracing.TraceIDKey, traceID)
		w.Header().Set("X-Trace-Id", traceID)

		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r.WithContext(ctx))

		slog.InfoContext(ctx, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"latency_ms", time.Since(start).Milliseconds(),
			"trace_id", traceID,
		)
	})
}
