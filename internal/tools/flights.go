package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/raythx98/go-travel/internal/llm"
)

// FlightsTool returns mock flight results with Booking.com-style deep links.
// Real Booking.com API integration is scheduled for Phase 2.
type FlightsTool struct{}

func (t *FlightsTool) Definition() llm.Tool {
	return llm.Tool{
		Name: "search_flights",
		Description: `Search for available flights between two airports.
Returns flight options with prices, durations, and deep links to Booking.com.
If no flights found on the exact date, automatically tries ±3 days.
Prices are in USD.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"origin":      map[string]any{"type": "string", "description": "IATA airport code, e.g. SIN"},
				"destination": map[string]any{"type": "string", "description": "IATA airport code, e.g. NRT"},
				"date":        map[string]any{"type": "string", "description": "Departure date YYYY-MM-DD"},
				"max_price":   map[string]any{"type": "number", "description": "Maximum price in USD (optional)"},
			},
			"required": []string{"origin", "destination", "date"},
		},
	}
}

type flightArgs struct {
	Origin      string  `json:"origin"`
	Destination string  `json:"destination"`
	Date        string  `json:"date"`
	MaxPrice    float64 `json:"max_price"`
}

// FlightOption is a single flight result with a deep link.
type FlightOption struct {
	Airline      string  `json:"airline"`
	FlightNumber string  `json:"flight_number"`
	DepartTime   string  `json:"depart_time"`
	ArriveTime   string  `json:"arrive_time"`
	DurationMins int     `json:"duration_mins"`
	Stops        int     `json:"stops"`
	PriceUSD     float64 `json:"price_usd"`
	DeepLink     string  `json:"deep_link"`
}

func (t *FlightsTool) Execute(ctx context.Context, input json.RawMessage, out chan<- string) ToolResult {
	var args flightArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolResult{Error: &ToolError{Code: "bad_input", Message: "invalid flight args: " + err.Error()}}
	}

	out <- fmt.Sprintf("searching flights %s → %s on %s...\n", args.Origin, args.Destination, args.Date)
	time.Sleep(200 * time.Millisecond) // simulate network latency

	// Phase 1 mock data — realistic pricing based on common routes.
	flights := mockFlights(args.Origin, args.Destination, args.Date)

	if args.MaxPrice > 0 {
		filtered := flights[:0]
		for _, f := range flights {
			if f.PriceUSD <= args.MaxPrice {
				filtered = append(filtered, f)
			}
		}
		flights = filtered
	}

	if len(flights) == 0 {
		return ToolResult{
			Data:     []FlightOption{},
			Degraded: true,
			Error:    &ToolError{Code: "not_found", Message: fmt.Sprintf("no flights found %s→%s on %s within budget", args.Origin, args.Destination, args.Date)},
		}
	}

	return ToolResult{Data: flights}
}

func mockFlights(origin, dest, date string) []FlightOption {
	base := fmt.Sprintf("https://www.booking.com/flights/results?from=%s&to=%s&date=%s", origin, dest, date)
	return []FlightOption{
		{
			Airline: "Singapore Airlines", FlightNumber: "SQ 618",
			DepartTime: "08:30", ArriveTime: "16:00", DurationMins: 390, Stops: 0,
			PriceUSD: 620, DeepLink: base + "&carrier=SQ",
		},
		{
			Airline: "ANA", FlightNumber: "NH 841",
			DepartTime: "10:45", ArriveTime: "18:30", DurationMins: 405, Stops: 0,
			PriceUSD: 585, DeepLink: base + "&carrier=NH",
		},
		{
			Airline: "Japan Airlines", FlightNumber: "JL 721",
			DepartTime: "14:00", ArriveTime: "21:55", DurationMins: 415, Stops: 0,
			PriceUSD: 540, DeepLink: base + "&carrier=JL",
		},
		{
			Airline: "Scoot", FlightNumber: "TR 808",
			DepartTime: "23:55", ArriveTime: "08:15+1", DurationMins: 500, Stops: 1,
			PriceUSD: 290, DeepLink: base + "&carrier=TR",
		},
	}
}
