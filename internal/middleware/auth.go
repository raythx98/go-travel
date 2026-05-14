package middleware

import (
	"log/slog"
	"net/http"

	"github.com/raythx98/go-travel/internal/tracing"
)

// Auth is a stub middleware that logs the Authorization header without enforcing it.
// Phase 4 will replace this with JWT validation.
func Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := tracing.FromContext(r.Context())
		if auth := r.Header.Get("Authorization"); auth != "" {
			slog.DebugContext(r.Context(), "auth_stub",
				"has_token", true,
				"trace_id", traceID,
			)
		}
		next.ServeHTTP(w, r)
	})
}
