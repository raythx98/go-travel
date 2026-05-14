// Package agent implements the travel planner ReAct loop.
// It has no dependency on any agent framework — the loop is plain Go.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/raythx98/go-travel/internal/event"
	"github.com/raythx98/go-travel/internal/itinerary"
	"github.com/raythx98/go-travel/internal/llm"
	"github.com/raythx98/go-travel/internal/tools"
	"github.com/raythx98/go-travel/internal/tracing"
)

// ErrMaxStepsReached is returned when the agent exceeds the step budget.
var ErrMaxStepsReached = errors.New("agent: max steps reached without finalizing itinerary")

const maxSteps = 20

// systemPrompt instructs Claude how to behave as the travel planning supervisor.
const systemPrompt = `You are an expert travel planning assistant. Your job is to create detailed,
practical travel itineraries tailored to the user's goal, budget, and preferences.

You have access to the following tools:
- geocode: Convert place names to coordinates (use before get_weather)
- get_weather: Fetch weather forecast for travel dates
- web_search: Search the web for up-to-date info (visas, prices, tips, transport)
- search_flights: Find flight options with prices and Booking.com deep links
- search_hotels: Find hotel options with prices and Booking.com deep links
- ask_user: Pause and ask the user a clarifying question (use sparingly, group questions)
- finalize_itinerary: Submit the completed itinerary (call this last, only once)

Planning approach:
1. If critical information is missing (travel dates, number of travellers, budget), call ask_user ONCE with all questions grouped together.
2. Geocode the destination to get coordinates for weather lookup.
3. Fetch weather for the travel dates.
4. Search for flights and hotels in parallel reasoning.
5. Use web_search for visa requirements, local tips, and anything time-sensitive.
6. Build a day-by-day plan that matches the weather and budget.
7. Call finalize_itinerary with the complete structured plan.

Rules:
- Always include Booking.com deep links for flights and hotels.
- Budget estimates must be realistic and broken down by category.
- If a tool fails or returns no results, reason around it — don't stop.
- Never call finalize_itinerary until the plan is complete.
- Call finalize_itinerary exactly once.`

// Agent runs the ReAct loop for a single planning session.
type Agent struct {
	llm   llm.LLMClient
	tools *tools.Registry
}

// New returns an Agent with the given LLM client and tool registry.
func New(client llm.LLMClient, registry *tools.Registry) *Agent {
	return &Agent{llm: client, tools: registry}
}

// Run executes the ReAct loop for the given goal. It writes streaming status
// updates to out and sends the final itinerary to itin (if non-nil).
// The caller must close out after Run returns.
func (a *Agent) Run(ctx context.Context, goal string, out chan<- string, itin chan *itinerary.Itinerary) error {
	traceID := tracing.FromContext(ctx)

	ctx = tools.WithItineraryChan(ctx, itin)

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: goal}}},
	}

	out <- event.Encode(event.Token{Type: "token", Content: fmt.Sprintf("Starting plan for: %s\n", goal)})

	for step := range maxSteps {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		slog.InfoContext(ctx, "agent_step",
			"step", step+1,
			"messages", len(messages),
			"trace_id", traceID,
		)

		resp, err := a.llm.Complete(ctx, systemPrompt, messages, a.tools.Definitions())
		if err != nil {
			return fmt.Errorf("agent step %d: %w", step+1, err)
		}

		// Stream any text content the model produced.
		for _, block := range resp.Content {
			if block.Type == "text" && block.Text != "" {
				out <- event.Encode(event.Token{Type: "token", Content: block.Text})
			}
		}

		switch resp.StopReason {
		case llm.StopReasonEndTurn:
			out <- event.Encode(event.Token{Type: "token", Content: "Planning complete.\n"})
			return nil

		case llm.StopReasonToolUse:
			// Check if the agent called finalize_itinerary.
			for _, block := range resp.Content {
				if block.Type == "tool_use" && block.ToolName == "finalize_itinerary" {
					results := a.tools.Execute(ctx, resp.Content, out)
					messages = append(messages,
						llm.Message{Role: llm.RoleAssistant, Content: resp.Content},
						llm.Message{Role: llm.RoleUser, Content: results},
					)
					out <- event.Encode(event.Token{Type: "token", Content: "Itinerary finalized.\n"})
					return nil
				}
			}

			// Regular tool calls — dispatch, collect results, continue loop.
			results := a.tools.Execute(ctx, resp.Content, out)
			messages = append(messages,
				llm.Message{Role: llm.RoleAssistant, Content: resp.Content},
				llm.Message{Role: llm.RoleUser, Content: results},
			)

		default:
			out <- event.Encode(event.Token{Type: "token", Content: fmt.Sprintf("Stopped: %s\n", resp.StopReason)})
			return nil
		}
	}

	return ErrMaxStepsReached
}
