// Package llm defines the LLMClient interface and shared message types used
// by all agents. Implementations (Claude, Ollama) live in the same package.
// Agents always reference LLMClient — never a concrete implementation.
package llm

import "context"

// Role is the conversational role of a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// StopReason indicates why the model stopped generating.
type StopReason string

const (
	StopReasonEndTurn   StopReason = "end_turn"
	StopReasonToolUse   StopReason = "tool_use"
	StopReasonMaxTokens StopReason = "max_tokens"
)

// ContentBlock is a single piece of content within a message.
// The Type field determines which other fields are populated.
type ContentBlock struct {
	// "text" | "tool_use" | "tool_result"
	Type string

	// type=text
	Text string

	// type=tool_use
	ToolUseID        string
	ToolName         string
	Input            []byte // raw JSON
	ThoughtSignature []byte // Gemini thinking models attach this; must be echoed back in history

	// type=tool_result
	Content string
	IsError bool
}

// Message is a conversational turn, either from the user or assistant.
type Message struct {
	Role    Role
	Content []ContentBlock
}

// Tool describes a function the model may call.
type Tool struct {
	Name        string
	Description string
	// InputSchema is a JSON Schema object describing the tool's input.
	InputSchema map[string]any
}

// Response is the model's reply to a Complete call.
type Response struct {
	Content      []ContentBlock
	StopReason   StopReason
	InputTokens  int64
	OutputTokens int64
	Model        string
}

// LLMClient is the single interface all agents use to communicate with a language model.
// Concrete implementations include ClaudeClient (primary) and OllamaClient (fallback).
type LLMClient interface {
	Complete(ctx context.Context, system string, messages []Message, tools []Tool) (Response, error)
}
