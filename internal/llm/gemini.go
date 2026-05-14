package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/genai"
	"github.com/raythx98/go-travel/internal/tracing"
)

const geminiDefaultModel = "gemini-2.0-flash"
const geminiMaxOutputTokens = 8192

// GeminiClient implements LLMClient using the Google Gemini API.
type GeminiClient struct {
	client *genai.Client
	model  string
}

// NewGeminiClient creates a GeminiClient authenticated with the given API key.
// If model is empty, gemini-2.0-flash is used.
func NewGeminiClient(ctx context.Context, apiKey, model string) (*GeminiClient, error) {
	if model == "" {
		model = geminiDefaultModel
	}
	c, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})
	if err != nil {
		return nil, fmt.Errorf("gemini: new client: %w", err)
	}
	return &GeminiClient{client: c, model: model}, nil
}

// Complete sends messages to Gemini and returns the response.
func (c *GeminiClient) Complete(ctx context.Context, system string, messages []Message, tools []Tool) (Response, error) {
	start := time.Now()
	traceID := tracing.FromContext(ctx)

	contents := toGeminiContents(messages)
	cfg := &genai.GenerateContentConfig{
		MaxOutputTokens: geminiMaxOutputTokens,
	}
	if system != "" {
		cfg.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: system}},
		}
	}
	if len(tools) > 0 {
		cfg.Tools = toGeminiTools(tools)
	}

	resp, err := c.client.Models.GenerateContent(ctx, c.model, contents, cfg)
	if err != nil {
		slog.ErrorContext(ctx, "gemini_error", "error", err, "trace_id", traceID)
		return Response{}, fmt.Errorf("gemini: %w", err)
	}

	result := fromGeminiResponse(resp, c.model)

	slog.InfoContext(ctx, "llm_call",
		"model", c.model,
		"input_tokens", result.InputTokens,
		"output_tokens", result.OutputTokens,
		"stop_reason", result.StopReason,
		"latency_ms", time.Since(start).Milliseconds(),
		"trace_id", traceID,
		"agent", "gemini",
	)

	return result, nil
}

// ShouldFallback returns true when the error warrants an Ollama fallback.
// Any Gemini API error (4xx, 5xx) triggers fallback — this covers rate limits,
// quota exhaustion, thought-signature rejections, and model unavailability.
// Context cancellation is excluded so user interrupts do not trigger fallback.
func ShouldFallback(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// toGeminiContents translates internal []Message to Gemini's []*genai.Content.
func toGeminiContents(messages []Message) []*genai.Content {
	var out []*genai.Content
	for _, m := range messages {
		role := "user"
		if m.Role == RoleAssistant {
			role = "model"
		}

		var parts []*genai.Part
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				parts = append(parts, &genai.Part{Text: b.Text})
			case "tool_use":
				var args map[string]any
				_ = json.Unmarshal(b.Input, &args)
				p := genai.NewPartFromFunctionCall(b.ToolName, args)
				p.ThoughtSignature = b.ThoughtSignature
				parts = append(parts, p)
			case "tool_result":
				// Tool results travel in user turns as FunctionResponse parts.
				var response map[string]any
				if b.IsError {
					response = map[string]any{"error": b.Content}
				} else {
					response = map[string]any{"output": b.Content}
				}
				parts = append(parts, genai.NewPartFromFunctionResponse(b.ToolUseID, response))
			}
		}

		if len(parts) > 0 {
			out = append(out, &genai.Content{Role: role, Parts: parts})
		}
	}
	return out
}

// toGeminiTools translates internal []Tool to Gemini's []*genai.Tool.
// Uses ParametersJsonSchema (accepts any) to avoid needing to build genai.Schema objects.
func toGeminiTools(tools []Tool) []*genai.Tool {
	decls := make([]*genai.FunctionDeclaration, len(tools))
	for i, t := range tools {
		decls[i] = &genai.FunctionDeclaration{
			Name:                 t.Name,
			Description:          t.Description,
			ParametersJsonSchema: t.InputSchema,
		}
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}

// fromGeminiResponse translates a Gemini response to our internal Response type.
func fromGeminiResponse(resp *genai.GenerateContentResponse, model string) Response {
	if len(resp.Candidates) == 0 {
		return Response{StopReason: StopReasonEndTurn, Model: model}
	}

	candidate := resp.Candidates[0]
	var blocks []ContentBlock
	hasFunctionCall := false

	if candidate.Content != nil {
		for _, part := range candidate.Content.Parts {
			switch {
			case part.Text != "" && !part.Thought:
				// Thought-only text parts must not be included in conversation history.
				blocks = append(blocks, ContentBlock{Type: "text", Text: part.Text})
			case part.FunctionCall != nil:
				hasFunctionCall = true
				input, _ := json.Marshal(part.FunctionCall.Args)
				// Use function call name as ID since Gemini may not always populate ID.
				id := part.FunctionCall.ID
				if id == "" {
					id = part.FunctionCall.Name
				}
				blocks = append(blocks, ContentBlock{
					Type:             "tool_use",
					ToolUseID:        id,
					ToolName:         part.FunctionCall.Name,
					Input:            input,
					ThoughtSignature: part.ThoughtSignature,
				})
			}
		}
	}

	stopReason := StopReasonEndTurn
	if hasFunctionCall {
		stopReason = StopReasonToolUse
	} else if candidate.FinishReason == genai.FinishReasonMaxTokens {
		stopReason = StopReasonMaxTokens
	}

	var inputTokens, outputTokens int64
	if resp.UsageMetadata != nil {
		inputTokens = int64(resp.UsageMetadata.PromptTokenCount)
		outputTokens = int64(resp.UsageMetadata.CandidatesTokenCount)
	}

	return Response{
		Content:      blocks,
		StopReason:   stopReason,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Model:        model,
	}
}
