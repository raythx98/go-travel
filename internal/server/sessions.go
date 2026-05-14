package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// sessionStatus is the lifecycle state of a planning session.
type sessionStatus string

const (
	sessionStatusRunning sessionStatus = "running"
	sessionStatusDone    sessionStatus = "done"
	sessionStatusError   sessionStatus = "error"
)

// session holds the in-memory state for one active or completed planning run.
type session struct {
	goal      string
	status    sessionStatus
	outCh     chan string     // SSE event channel; closed by the agent goroutine when done
	respChan  chan string     // receives user answers to ask_user questions
	cancel    context.CancelFunc
	createdAt time.Time
}

// sessionView is the JSON-serialisable representation returned by the REST endpoints.
type sessionView struct {
	ID        string    `json:"id"`
	Goal      string    `json:"goal"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// sessionStore is a thread-safe in-memory map of sessions.
// Phase 2 replaces this with a Postgres-backed store.
type sessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*session
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]*session)}
}

// register adds a pre-constructed session to the store.
func (s *sessionStore) register(id string, sess *session) {
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
}

// get returns the session for id, or false if not found.
func (s *sessionStore) get(id string) (*session, bool) {
	s.mu.RLock()
	sess, ok := s.sessions[id]
	s.mu.RUnlock()
	return sess, ok
}

// view returns a serialisable snapshot of one session, or false if not found.
func (s *sessionStore) view(id string) (sessionView, bool) {
	s.mu.RLock()
	sess, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return sessionView{}, false
	}
	s.mu.RLock()
	v := sessionView{ID: id, Goal: sess.goal, Status: string(sess.status), CreatedAt: sess.createdAt}
	s.mu.RUnlock()
	return v, true
}

// list returns all sessions as serialisable snapshots, newest first.
func (s *sessionStore) list() []sessionView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]sessionView, 0, len(s.sessions))
	for id, sess := range s.sessions {
		out = append(out, sessionView{
			ID:        id,
			Goal:      sess.goal,
			Status:    string(sess.status),
			CreatedAt: sess.createdAt,
		})
	}
	return out
}

// setStatus updates the status of an existing session.
func (s *sessionStore) setStatus(id string, status sessionStatus) {
	s.mu.Lock()
	if sess, ok := s.sessions[id]; ok {
		sess.status = status
	}
	s.mu.Unlock()
}

// respond sends answer to the ask_user tool waiting on the session.
func (s *sessionStore) respond(id, answer string) error {
	s.mu.RLock()
	sess, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	select {
	case sess.respChan <- answer:
		return nil
	default:
		return fmt.Errorf("session %s is not waiting for a response", id)
	}
}

func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
