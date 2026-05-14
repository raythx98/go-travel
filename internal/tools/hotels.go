package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/raythx98/go-travel/internal/llm"
)

// HotelsTool returns mock hotel results with Booking.com-style deep links.
// Real Booking.com API integration is scheduled for Phase 2.
type HotelsTool struct{}

func (t *HotelsTool) Definition() llm.Tool {
	return llm.Tool{
		Name: "search_hotels",
		Description: `Search for hotels, apartments, and accommodation options in a city.
Returns options with prices per night, ratings, and deep links to Booking.com.
Prices are in USD per night.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city":          map[string]any{"type": "string", "description": "City name, e.g. Kyoto"},
				"checkin_date":  map[string]any{"type": "string", "description": "Check-in date YYYY-MM-DD"},
				"checkout_date": map[string]any{"type": "string", "description": "Check-out date YYYY-MM-DD"},
				"max_price":     map[string]any{"type": "number", "description": "Maximum price per night in USD (optional)"},
				"guests":        map[string]any{"type": "integer", "description": "Number of guests (default 2)"},
			},
			"required": []string{"city", "checkin_date", "checkout_date"},
		},
	}
}

type hotelArgs struct {
	City         string  `json:"city"`
	CheckinDate  string  `json:"checkin_date"`
	CheckoutDate string  `json:"checkout_date"`
	MaxPrice     float64 `json:"max_price"`
	Guests       int     `json:"guests"`
}

// HotelOption is a single hotel result with a deep link.
type HotelOption struct {
	Name          string  `json:"name"`
	Stars         int     `json:"stars"`
	ReviewScore   float64 `json:"review_score"`
	ReviewCount   int     `json:"review_count"`
	PricePerNight float64 `json:"price_per_night_usd"`
	Neighbourhood string  `json:"neighbourhood"`
	DeepLink      string  `json:"deep_link"`
}

func (t *HotelsTool) Execute(ctx context.Context, input json.RawMessage, out chan<- string) ToolResult {
	var args hotelArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolResult{Error: &ToolError{Code: "bad_input", Message: "invalid hotel args: " + err.Error()}}
	}
	if args.Guests == 0 {
		args.Guests = 2
	}

	out <- fmt.Sprintf("searching hotels in %s (%s – %s)...\n", args.City, args.CheckinDate, args.CheckoutDate)
	time.Sleep(200 * time.Millisecond)

	hotels := mockHotels(args.City, args.CheckinDate, args.CheckoutDate)

	if args.MaxPrice > 0 {
		filtered := hotels[:0]
		for _, h := range hotels {
			if h.PricePerNight <= args.MaxPrice {
				filtered = append(filtered, h)
			}
		}
		hotels = filtered
	}

	if len(hotels) == 0 {
		return ToolResult{
			Data:     []HotelOption{},
			Degraded: true,
			Error:    &ToolError{Code: "not_found", Message: fmt.Sprintf("no hotels found in %s within budget", args.City)},
		}
	}

	return ToolResult{Data: hotels}
}

func mockHotels(city, checkin, checkout string) []HotelOption {
	base := fmt.Sprintf("https://www.booking.com/searchresults.html?ss=%s&checkin=%s&checkout=%s",
		city, checkin, checkout)
	return []HotelOption{
		{
			Name: "The Ritz-Carlton, " + city, Stars: 5, ReviewScore: 9.4, ReviewCount: 1820,
			PricePerNight: 380, Neighbourhood: "City Centre",
			DeepLink: base + "&hotel=ritz_carlton",
		},
		{
			Name: city + " Marriott Hotel", Stars: 4, ReviewScore: 8.8, ReviewCount: 3241,
			PricePerNight: 185, Neighbourhood: "Downtown",
			DeepLink: base + "&hotel=marriott",
		},
		{
			Name: "APA Hotel " + city, Stars: 3, ReviewScore: 8.1, ReviewCount: 5670,
			PricePerNight: 85, Neighbourhood: "Station Area",
			DeepLink: base + "&hotel=apa",
		},
		{
			Name: city + " Guesthouse", Stars: 2, ReviewScore: 7.9, ReviewCount: 890,
			PricePerNight: 45, Neighbourhood: "Old Town",
			DeepLink: base + "&hotel=guesthouse",
		},
		{
			Name: "Airbnb Apartment — Central " + city, Stars: 0, ReviewScore: 4.8, ReviewCount: 312,
			PricePerNight: 110, Neighbourhood: "Central",
			DeepLink: base + "&airbnb=central_apt",
		},
	}
}
