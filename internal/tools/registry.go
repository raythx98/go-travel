package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/raythx98/go-travel/internal/event"
	"github.com/raythx98/go-travel/internal/llm"
	"github.com/raythx98/go-travel/internal/tracing"
)

// Handler is the interface every tool must implement.
type Handler interface {
	// Definition returns the tool's name, description, and JSON Schema for Claude.
	Definition() llm.Tool
	// Execute runs the tool and returns a typed result. It may write status lines
	// to out for streaming to the client.
	Execute(ctx context.Context, input json.RawMessage, out chan<- string) ToolResult
}

// Registry holds all registered tools and dispatches calls from the agent loop.
type Registry struct {
	handlers map[string]Handler
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

// Register adds a tool to the registry.
func (r *Registry) Register(h Handler) {
	r.handlers[h.Definition().Name] = h
}

// Definitions returns the tool list to pass to the LLM.
func (r *Registry) Definitions() []llm.Tool {
	tools := make([]llm.Tool, 0, len(r.handlers))
	for _, h := range r.handlers {
		tools = append(tools, h.Definition())
	}
	return tools
}

// Execute dispatches all tool_use blocks in calls, streams status for each,
// and returns a slice of tool_result content blocks for the next LLM message.
func (r *Registry) Execute(ctx context.Context, calls []llm.ContentBlock, out chan<- string) []llm.ContentBlock {
	traceID := tracing.FromContext(ctx)
	var results []llm.ContentBlock

	for _, call := range calls {
		if call.Type != "tool_use" {
			continue
		}

		out <- event.Encode(event.Tool{Type: "tool", Name: call.ToolName, Status: "running"})

		h, ok := r.handlers[call.ToolName]
		if !ok {
			slog.WarnContext(ctx, "unknown tool", "tool", call.ToolName, "trace_id", traceID)
			results = append(results, llm.ContentBlock{
				Type:      "tool_result",
				ToolUseID: call.ToolUseID,
				Content:   fmt.Sprintf("unknown tool: %s", call.ToolName),
				IsError:   true,
			})
			out <- event.Encode(event.Tool{Type: "tool", Name: call.ToolName, Status: "error"})
			continue
		}

		result := h.Execute(ctx, call.Input, out)

		slog.InfoContext(ctx, "tool_result",
			"tool", call.ToolName,
			"degraded", result.Degraded,
			"error", result.Error,
			"trace_id", traceID,
		)

		var content string
		if result.Error != nil {
			content = result.Error.Message
			out <- event.Encode(event.Tool{Type: "tool", Name: call.ToolName, Status: "error"})
			results = append(results, llm.ContentBlock{
				Type:      "tool_result",
				ToolUseID: call.ToolUseID,
				Content:   content,
				IsError:   true,
			})
			continue
		}

		b, _ := json.Marshal(result.Data)
		content = string(b)

		out <- event.Encode(event.Tool{Type: "tool", Name: call.ToolName, Status: "done"})

		results = append(results, llm.ContentBlock{
			Type:      "tool_result",
			ToolUseID: call.ToolUseID,
			Content:   content,
		})
	}

	return results
}
