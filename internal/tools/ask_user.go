package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/raythx98/go-travel/internal/event"
	"github.com/raythx98/go-travel/internal/llm"
)

type respChanCtxKey struct{}

// WithRespChan injects a response channel into ctx so AskUserTool can receive answers.
// Called by the session handler before starting the agent run.
func WithRespChan(ctx context.Context, ch chan string) context.Context {
	return context.WithValue(ctx, respChanCtxKey{}, ch)
}

// AskUserTool allows the agent to pause and ask the user a clarifying question.
// The question is emitted as a JSON SSE event; the answer arrives via the respond endpoint.
type AskUserTool struct{}

// Definition returns the tool's LLM schema.
func (t *AskUserTool) Definition() llm.Tool {
	return llm.Tool{
		Name: "ask_user",
		Description: `Pause and ask the user a clarifying question when you need information
that's not in the original request and that significantly affects the plan.
Group multiple questions into one call — ask everything at once, not one at a time.
Do NOT use this for minor details you can reasonably assume.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{"type": "string", "description": "The question(s) to ask the user"},
			},
			"required": []string{"question"},
		},
	}
}

type askUserArgs struct {
	Question string `json:"question"`
}

// Execute emits a question event and blocks until the user responds or the context is cancelled.
func (t *AskUserTool) Execute(ctx context.Context, input json.RawMessage, out chan<- string) ToolResult {
	var args askUserArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolResult{Error: &ToolError{Code: "bad_input", Message: "invalid ask_user args: " + err.Error()}}
	}

	qID := newQuestionID()
	out <- event.Encode(event.Question{Type: "question", ID: qID, Text: args.Question})

	ch, ok := ctx.Value(respChanCtxKey{}).(chan string)
	if !ok {
		return ToolResult{Data: "No response received. Please continue with reasonable assumptions."}
	}

	// Wait up to 5 minutes for the user to respond, then continue with assumptions.
	select {
	case answer := <-ch:
		return ToolResult{Data: answer}
	case <-time.After(5 * time.Minute):
		return ToolResult{
			Data:     "User did not respond within 5 minutes. Continue with reasonable assumptions.",
			Degraded: true,
		}
	case <-ctx.Done():
		return ToolResult{Data: "Request cancelled.", Degraded: true}
	}
}

func newQuestionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
