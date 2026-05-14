// Package tools provides the tool registry, retry logic, and all tool implementations.
package tools

// ToolResult wraps every tool's output so the agent can reason about failures
// without receiving raw API responses.
type ToolResult struct {
	// Data holds the successful result (any JSON-serialisable value).
	Data any
	// Error is non-nil when the tool failed.
	Error *ToolError
	// Degraded is true when the tool returned partial data.
	Degraded bool
}

// ToolError carries structured failure information.
type ToolError struct {
	// Code is a short machine-readable string: "rate_limited" | "unavailable" | "bad_input" | "timeout"
	Code    string
	Message string
	// Retryable is false for 401/400 errors; true for transient failures.
	Retryable bool
}

func (e *ToolError) Error() string { return e.Code + ": " + e.Message }

// Summary returns a short string the agent can read as a tool result.
func (r ToolResult) Summary() string {
	if r.Error != nil {
		return "error: " + r.Error.Message
	}
	return "ok"
}
