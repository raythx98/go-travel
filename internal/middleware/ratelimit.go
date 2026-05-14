package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/raythx98/go-travel/internal/tracing"
	"github.com/redis/go-redis/v9"
)

// RateLimit returns middleware that enforces a sliding-window per-IP request limit
// using Redis. Requests exceeding the limit receive 429 with a Retry-After header.
// If Redis is unavailable, requests are allowed through (fail-open).
func RateLimit(rdb *redis.Client, rpm int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			traceID := tracing.FromContext(r.Context())

			key := fmt.Sprintf("rl:%s", ip)
			ctx, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
			defer cancel()

			count, err := rdb.Incr(ctx, key).Result()
			if err != nil {
				// Redis unavailable — fail open to avoid blocking legitimate traffic.
				slog.WarnContext(r.Context(), "ratelimit_redis_error",
					"error", err,
					"trace_id", traceID,
				)
				next.ServeHTTP(w, r)
				return
			}

			if count == 1 {
				rdb.Expire(ctx, key, time.Minute)
			}

			if count > int64(rpm) {
				slog.WarnContext(r.Context(), "rate_limited",
					"ip", ip,
					"count", count,
					"limit", rpm,
					"trace_id", traceID,
				)
				w.Header().Set("Retry-After", "60")
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the real client IP, respecting X-Forwarded-For from Caddy.
func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		return forwarded
	}
	if forwarded := r.Header.Get("X-Real-Ip"); forwarded != "" {
		return forwarded
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
