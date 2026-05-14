package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/raythx98/go-travel/internal/event"
	"github.com/raythx98/go-travel/internal/itinerary"
	"github.com/raythx98/go-travel/internal/tools"
	"github.com/raythx98/go-travel/internal/tracing"
)

// handleCreateSession creates a new planning session, starts the agent goroutine,
// and immediately returns {id} as JSON. The SSE stream is opened separately via
// GET /sessions/{id}/stream.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Goal string `json:"goal"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Goal == "" {
		http.Error(w, "goal is required", http.StatusBadRequest)
		return
	}

	id := newSessionID()
	outCh := make(chan string, 512)
	respChan := make(chan string, 1)

	ctx, cancel := context.WithCancel(context.Background())
	ctx = tools.WithRespChan(ctx, respChan)

	sess := &session{
		goal:      req.Goal,
		status:    sessionStatusRunning,
		outCh:     outCh,
		respChan:  respChan,
		cancel:    cancel,
		createdAt: time.Now(),
	}
	s.sessions.register(id, sess)

	traceID := tracing.FromContext(r.Context())

	go func() {
		itinCh := make(chan *itinerary.Itinerary, 1)
		err := s.agent.Run(ctx, req.Goal, outCh, itinCh)

		// Drain the itinerary channel (may be populated by the finalize tool).
		select {
		case <-itinCh:
		default:
		}

		if err != nil && !errors.Is(err, context.Canceled) {
			slog.ErrorContext(ctx, "agent_error", "error", err, "session_id", id, "trace_id", traceID)
			outCh <- event.Encode(event.Error{Type: "error", Message: err.Error()})
			s.sessions.setStatus(id, sessionStatusError)
		} else {
			s.sessions.setStatus(id, sessionStatusDone)
		}
		outCh <- event.Encode(event.Done{Type: "done", SessionID: id})
		close(outCh)
		cancel()
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
}

// handleStreamSession streams SSE events from an active session's output channel.
func (s *Server) handleStreamSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.sessions.get(id)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case line, open := <-sess.outCh:
			if !open {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}
	}
}

// handleListSessions returns all in-memory sessions as JSON.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	views := s.sessions.list()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(views)
}

// handleGetSession returns a single session by ID.
func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v, ok := s.sessions.view(id)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// handleRespondToQuestion injects a user answer into an active ask_user pause.
// The question_id field is accepted but not used for routing — there can only be
// one pending question per session at a time.
func (s *Server) handleRespondToQuestion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req struct {
		QuestionID string `json:"question_id"`
		Answer     string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Answer == "" {
		http.Error(w, "answer is required", http.StatusBadRequest)
		return
	}

	if err := s.sessions.respond(id, req.Answer); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleInterruptSession cancels a running session's context, stopping the agent.
func (s *Server) handleInterruptSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.sessions.get(id)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	sess.cancel()
	w.WriteHeader(http.StatusNoContent)
}

// handleHealth returns 200 OK for load balancer health checks.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintln(w, `{"status":"ok"}`)
}
