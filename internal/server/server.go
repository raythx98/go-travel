// Package server wires up the HTTP router and middleware stack.
package server

import (
	"net/http"

	"github.com/raythx98/go-travel/internal/agent"
	"github.com/raythx98/go-travel/internal/langfuse"
	"github.com/raythx98/go-travel/internal/middleware"
	"github.com/redis/go-redis/v9"
)

// Server holds shared dependencies available to all HTTP handlers.
type Server struct {
	agent    *agent.Agent
	sessions *sessionStore
	langfuse *langfuse.Client
}

// New constructs a Server and returns the root http.Handler with all middleware applied.
// rdb may be nil — rate limiting is skipped when Redis is unavailable.
func New(a *agent.Agent, lf *langfuse.Client, rdb *redis.Client, allowedOrigin string, rateLimitRPM int) http.Handler {
	s := &Server{
		agent:    a,
		sessions: newSessionStore(),
		langfuse: lf,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /sessions", s.handleCreateSession)
	mux.HandleFunc("GET /sessions", s.handleListSessions)
	mux.HandleFunc("GET /sessions/{id}", s.handleGetSession)
	mux.HandleFunc("GET /sessions/{id}/stream", s.handleStreamSession)
	mux.HandleFunc("POST /sessions/{id}/respond", s.handleRespondToQuestion)
	mux.HandleFunc("POST /sessions/{id}/interrupt", s.handleInterruptSession)

	// Middleware: recovery → rate-limit → logging → auth → cors → mux
	var h http.Handler = mux
	h = middleware.CORS(allowedOrigin)(h)
	h = middleware.Auth(h)
	h = middleware.Logging(h)
	if rdb != nil {
		h = middleware.RateLimit(rdb, rateLimitRPM)(h)
	}
	h = middleware.Recovery(h)

	return h
}
