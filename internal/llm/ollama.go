package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/raythx98/go-travel/internal/tracing"
)

const ollamaDefaultModel = "llama3.2"

// OllamaClient implements LLMClient using Ollama's /api/chat endpoint.
// It is used as the fallback when Claude is rate-limited or unavailable.
type OllamaClient struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewOllamaClient returns an OllamaClient pointing at the given base URL.
// If model is empty, llama3.2 is used.
func NewOllamaClient(baseURL, model string) *OllamaClient {
	if model == "" {
		model = ollamaDefaultModel
	}
	return &OllamaClient{
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// ollamaMessage mirrors Ollama's chat message format.
type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaToolCall struct {
	Function ollamaToolCallFunc `json:"function"`
}

type ollamaToolCallFunc struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ollamaTool struct {
	Type     string         `json:"type"`
	Function ollamaToolFunc `json:"function"`
}

type ollamaToolFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Tools    []ollamaTool    `json:"tools,omitempty"`
	Stream   bool            `json:"stream"`
}

type ollamaChatResponse struct {
	Message         ollamaMessage `json:"message"`
	DoneReason      string        `json:"done_reason"`
	Done            bool          `json:"done"`
	EvalCount       int64         `json:"eval_count"`
	PromptEvalCount int64         `json:"prompt_eval_count"`
}

// Complete sends messages to Ollama and returns the response.
func (c *OllamaClient) Complete(ctx context.Context, system string, messages []Message, tools []Tool) (Response, error) {
	start := time.Now()
	traceID := tracing.FromContext(ctx)

	ollamaMsgs := toOllamaMessages(system, messages)
	ollamaTools := toOllamaTools(tools)

	reqBody := ollamaChatRequest{
		Model:    c.model,
		Messages: ollamaMsgs,
		Tools:    ollamaTools,
		Stream:   false,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return Response{}, fmt.Errorf("ollama: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("ollama: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("ollama: returned %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("ollama: read response: %w", err)
	}

	var ollamaResp ollamaChatResponse
	if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
		return Response{}, fmt.Errorf("ollama: decode response: %w", err)
	}

	result := fromOllamaResponse(ollamaResp)

	slog.InfoContext(ctx, "llm_call",
		"model", c.model,
		"input_tokens", result.InputTokens,
		"output_tokens", result.OutputTokens,
		"stop_reason", result.StopReason,
		"latency_ms", time.Since(start).Milliseconds(),
		"trace_id", traceID,
		"agent", "ollama",
	)

	return result, nil
}

func toOllamaMessages(system string, messages []Message) []ollamaMessage {
	var out []ollamaMessage
	if system != "" {
		out = append(out, ollamaMessage{Role: "system", Content: system})
	}
	for _, m := range messages {
		var content string
		var toolCalls []ollamaToolCall

		for _, b := range m.Content {
			switch b.Type {
			case "text":
				content += b.Text
			case "tool_use":
				toolCalls = append(toolCalls, ollamaToolCall{
					Function: ollamaToolCallFunc{Name: b.ToolName, Arguments: b.Input},
				})
			case "tool_result":
				// Tool results become user messages in Ollama
				content += b.Content
			}
		}

		role := string(m.Role)
		if len(toolCalls) > 0 {
			out = append(out, ollamaMessage{Role: "assistant", ToolCalls: toolCalls})
		} else {
			out = append(out, ollamaMessage{Role: role, Content: content})
		}
	}
	return out
}

func toOllamaTools(tools []Tool) []ollamaTool {
	out := make([]ollamaTool, len(tools))
	for i, t := range tools {
		out[i] = ollamaTool{
			Type: "function",
			Function: ollamaToolFunc{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		}
	}
	return out
}

func fromOllamaResponse(resp ollamaChatResponse) Response {
	var blocks []ContentBlock
	msg := resp.Message

	if msg.Content != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: msg.Content})
	}

	for i, tc := range msg.ToolCalls {
		blocks = append(blocks, ContentBlock{
			Type:      "tool_use",
			ToolUseID: fmt.Sprintf("ollama_tool_%d", i),
			ToolName:  tc.Function.Name,
			Input:     tc.Function.Arguments,
		})
	}

	stopReason := StopReasonEndTurn
	if len(msg.ToolCalls) > 0 {
		stopReason = StopReasonToolUse
	}

	return Response{
		Content:      blocks,
		StopReason:   stopReason,
		InputTokens:  resp.PromptEvalCount,
		OutputTokens: resp.EvalCount,
		Model:        ollamaDefaultModel,
	}
}
