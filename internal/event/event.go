// Package event defines the SSE event types streamed to the client during an agent run.
package event

import "encoding/json"

// Encode marshals v to a JSON string ready to write as an SSE data line.
// Marshal errors are silently swallowed; a dropped event is preferable to
// crashing the streaming goroutine.
func Encode(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Token is a text chunk emitted while the agent is reasoning or summarising.
type Token struct {
	Type    string `json:"type"`    // always "token"
	Content string `json:"content"` // text fragment to append to the output
}

// Tool reports the running/done/error status of a single tool call.
type Tool struct {
	Type   string `json:"type"`   // always "tool"
	Name   string `json:"name"`   // tool name matching the registry
	Status string `json:"status"` // "running" | "done" | "error"
}

// Question is emitted when the agent needs additional input from the user.
type Question struct {
	Type string `json:"type"` // always "question"
	ID   string `json:"id"`   // opaque ID echoed back in the respond endpoint
	Text string `json:"text"` // the question text to display
}

// Done signals the session has completed successfully.
type Done struct {
	Type      string `json:"type"`       // always "done"
	SessionID string `json:"session_id"` // the session that finished
}

// Error signals an unrecoverable agent failure.
type Error struct {
	Type    string `json:"type"`    // always "error"
	Message string `json:"message"` // human-readable error description
}
