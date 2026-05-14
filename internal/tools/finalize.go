package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/raythx98/go-travel/internal/event"
	"github.com/raythx98/go-travel/internal/itinerary"
	"github.com/raythx98/go-travel/internal/llm"
)

type itineraryChanCtxKey struct{}

// WithItineraryChan injects a channel into ctx so FinalizeTool can deliver the
// structured itinerary back to the agent loop.
func WithItineraryChan(ctx context.Context, ch chan *itinerary.Itinerary) context.Context {
	return context.WithValue(ctx, itineraryChanCtxKey{}, ch)
}

// FinalizeTool is called by the agent to submit the completed, structured
// itinerary. It is the terminal tool call — the agent loop ends when this fires.
type FinalizeTool struct{}

func (t *FinalizeTool) Definition() llm.Tool {
	return llm.Tool{
		Name: "finalize_itinerary",
		Description: `Submit the completed travel itinerary. Call this ONLY when the full plan
is ready. This is the final action — do not call any other tools after this.
All days, activities, accommodation, transport, and budget must be filled in.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"destination": map[string]any{"type": "string", "description": "Primary destination"},
				"start_date":  map[string]any{"type": "string", "description": "Trip start date YYYY-MM-DD"},
				"end_date":    map[string]any{"type": "string", "description": "Trip end date YYYY-MM-DD"},
				"summary":     map[string]any{"type": "string", "description": "2-3 sentence trip overview"},
				"days": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"date":        map[string]any{"type": "string"},
							"title":       map[string]any{"type": "string"},
							"description": map[string]any{"type": "string"},
							"activities": map[string]any{
								"type": "array",
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"time":      map[string]any{"type": "string"},
										"name":      map[string]any{"type": "string"},
										"notes":     map[string]any{"type": "string"},
										"deep_link": map[string]any{"type": "string"},
									},
								},
							},
						},
					},
				},
				"accommodation": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":      map[string]any{"type": "string"},
						"deep_link": map[string]any{"type": "string"},
					},
				},
				"flights": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"direction": map[string]any{"type": "string", "description": "outbound or return"},
							"airline":   map[string]any{"type": "string"},
							"deep_link": map[string]any{"type": "string"},
						},
					},
				},
				"budget": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"total_usd":         map[string]any{"type": "number"},
						"flights_usd":       map[string]any{"type": "number"},
						"accommodation_usd": map[string]any{"type": "number"},
						"activities_usd":    map[string]any{"type": "number"},
						"food_usd":          map[string]any{"type": "number"},
						"misc_usd":          map[string]any{"type": "number"},
					},
				},
				"notes": map[string]any{"type": "string", "description": "Practical tips, packing notes, warnings"},
			},
			"required": []string{"destination", "start_date", "end_date", "summary", "days"},
		},
	}
}

func (t *FinalizeTool) Execute(ctx context.Context, input json.RawMessage, out chan<- string) ToolResult {
	var itin itinerary.Itinerary
	if err := json.Unmarshal(input, &itin); err != nil {
		return ToolResult{Error: &ToolError{Code: "bad_input", Message: "invalid itinerary: " + err.Error()}}
	}

	out <- event.Encode(event.Token{Type: "token", Content: formatItinerary(&itin)})

	ch, ok := ctx.Value(itineraryChanCtxKey{}).(chan *itinerary.Itinerary)
	if ok {
		ch <- &itin
	}

	return ToolResult{Data: "itinerary finalized"}
}

// formatItinerary renders an Itinerary as human-readable markdown.
func formatItinerary(itin *itinerary.Itinerary) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## %s  |  %s – %s\n\n", itin.Destination, itin.StartDate, itin.EndDate)

	if itin.Summary != "" {
		fmt.Fprintf(&b, "%s\n\n", itin.Summary)
	}

	for _, day := range itin.Days {
		fmt.Fprintf(&b, "---\n### %s · %s\n", day.Date, day.Title)
		if day.Description != "" {
			fmt.Fprintf(&b, "%s\n", day.Description)
		}
		for _, act := range day.Activities {
			line := fmt.Sprintf("- **%s** %s", act.Time, act.Name)
			if act.Notes != "" {
				line += " — " + act.Notes
			}
			if act.DeepLink != "" {
				line += fmt.Sprintf(" ([book](%s))", act.DeepLink)
			}
			fmt.Fprintln(&b, line)
		}
		fmt.Fprintln(&b)
	}

	if itin.Accommodation.Name != "" {
		fmt.Fprintf(&b, "**Accommodation:** %s", itin.Accommodation.Name)
		if itin.Accommodation.DeepLink != "" {
			fmt.Fprintf(&b, " ([book](%s))", itin.Accommodation.DeepLink)
		}
		fmt.Fprintln(&b)
	}

	if len(itin.Flights) > 0 {
		fmt.Fprintln(&b, "\n**Flights**")
		for _, f := range itin.Flights {
			line := fmt.Sprintf("- %s · %s", f.Direction, f.Airline)
			if f.DeepLink != "" {
				line += fmt.Sprintf(" ([book](%s))", f.DeepLink)
			}
			fmt.Fprintln(&b, line)
		}
	}

	if itin.Budget.TotalUSD > 0 {
		fmt.Fprintln(&b, "\n**Budget (USD)**")
		fmt.Fprintf(&b, "| | |\n|---|---|\n")
		fmt.Fprintf(&b, "| Flights | $%.0f |\n", itin.Budget.FlightsUSD)
		fmt.Fprintf(&b, "| Accommodation | $%.0f |\n", itin.Budget.AccommodationUSD)
		fmt.Fprintf(&b, "| Activities | $%.0f |\n", itin.Budget.ActivitiesUSD)
		fmt.Fprintf(&b, "| Food | $%.0f |\n", itin.Budget.FoodUSD)
		fmt.Fprintf(&b, "| Misc | $%.0f |\n", itin.Budget.MiscUSD)
		fmt.Fprintf(&b, "| **Total** | **$%.0f** |\n", itin.Budget.TotalUSD)
	}

	if itin.Notes != "" {
		fmt.Fprintf(&b, "\n**Notes**\n%s\n", itin.Notes)
	}

	return b.String()
}
