// Package tracing provides trace ID generation and context propagation.
package tracing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type contextKey string

// TraceIDKey is the context key for the per-request trace ID.
const TraceIDKey contextKey = "trace_id"

// NewID generates a random 16-byte hex trace ID.
func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// FromContext extracts the trace ID from ctx, returning an empty string if absent.
func FromContext(ctx context.Context) string {
	v, _ := ctx.Value(TraceIDKey).(string)
	return v
}
