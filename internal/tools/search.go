package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/raythx98/go-travel/internal/event"
	"github.com/raythx98/go-travel/internal/llm"
)

// SearchTool queries the self-hosted SearXNG instance for web search results.
type SearchTool struct {
	client     *http.Client
	searxngURL string
}

// NewSearchTool returns a SearchTool pointing at the given SearXNG base URL.
func NewSearchTool(searxngURL string) *SearchTool {
	return &SearchTool{
		client:     &http.Client{Timeout: 8 * time.Second},
		searxngURL: searxngURL,
	}
}

func (t *SearchTool) Definition() llm.Tool {
	return llm.Tool{
		Name: "web_search",
		Description: `Search the web for up-to-date information using SearXNG.
Use for: visa requirements, travel advisories, current events, local tips, opening hours,
pricing, transportation options, and anything that may have changed since training data.
Returns up to 5 results with title, URL, and snippet.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "The search query"},
			},
			"required": []string{"query"},
		},
	}
}

type searchArgs struct {
	Query string `json:"query"`
}

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

func (t *SearchTool) Execute(ctx context.Context, input json.RawMessage, out chan<- string) ToolResult {
	var args searchArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolResult{Error: &ToolError{Code: "bad_input", Message: "invalid search args: " + err.Error()}}
	}

	out <- event.Encode(event.Token{Type: "token", Content: fmt.Sprintf("Searching: %s\n", args.Query)})

	apiURL := t.searxngURL + "/search?q=" + url.QueryEscape(args.Query) + "&format=json&categories=general&engines=duckduckgo,bing"

	var result ToolResult
	err := withRetry(ctx, 3, func() error {
		reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, apiURL, nil)
		if err != nil {
			return err
		}

		resp, err := t.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return &httpError{statusCode: resp.StatusCode, message: fmt.Sprintf("searxng returned %d", resp.StatusCode)}
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		var raw struct {
			Results []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Content string `json:"content"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return err
		}

		limit := 5
		if len(raw.Results) < limit {
			limit = len(raw.Results)
		}
		results := make([]searchResult, limit)
		for i := range limit {
			results[i] = searchResult{
				Title:   raw.Results[i].Title,
				URL:     raw.Results[i].URL,
				Content: raw.Results[i].Content,
			}
		}
		result = ToolResult{Data: results}
		return nil
	})

	if err != nil {
		return ToolResult{
			Error:    &ToolError{Code: "unavailable", Message: "web search unavailable: " + err.Error(), Retryable: true},
			Degraded: true,
		}
	}
	return result
}
