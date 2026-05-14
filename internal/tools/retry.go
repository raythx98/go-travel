package tools

import (
	"context"
	"math"
	"time"
)

// withRetry calls fn up to maxAttempts times, backing off exponentially on
// retryable errors. Non-retryable errors (401, 400) are returned immediately.
func withRetry(ctx context.Context, maxAttempts int, fn func() error) error {
	var last error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		last = fn()
		if last == nil {
			return nil
		}
		if !isRetryable(last) {
			return last
		}
		wait := time.Duration(math.Pow(2, float64(attempt))) * time.Second
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return last
}

// isRetryable returns true for transient failures (network errors, 5xx, 429).
// HTTP 400 and 401 are not retryable.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	// All context errors are non-retryable after the first attempt
	if err == context.DeadlineExceeded || err == context.Canceled {
		return false
	}
	if he, ok := err.(*httpError); ok {
		return he.statusCode == 429 || he.statusCode >= 500
	}
	// Assume network errors are retryable
	return true
}

// httpError is a sentinel used by tools to signal an HTTP status code.
type httpError struct {
	statusCode int
	message    string
}

func (e *httpError) Error() string { return e.message }
