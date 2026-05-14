package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/raythx98/go-travel/internal/llm"
)

// GeocodeTool resolves place names to coordinates using Nominatim (OpenStreetMap).
// Nominatim requires a User-Agent header identifying the application.
type GeocodeTool struct {
	client    *http.Client
	userAgent string
}

// NewGeocodeTool returns a GeocodeTool configured with the required User-Agent.
func NewGeocodeTool() *GeocodeTool {
	return &GeocodeTool{
		client:    &http.Client{Timeout: 3 * time.Second},
		userAgent: "go-travel/1.0 (github.com/raythx98/go-travel)",
	}
}

func (t *GeocodeTool) Definition() llm.Tool {
	return llm.Tool{
		Name: "geocode",
		Description: `Convert a place name, city, or address to latitude and longitude coordinates.
Use before calling get_weather or any tool that needs coordinates.
Returns the best matching result with display name and coordinates.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Place name or address, e.g. 'Kyoto, Japan'"},
			},
			"required": []string{"query"},
		},
	}
}

type geocodeArgs struct {
	Query string `json:"query"`
}

type geocodeResult struct {
	DisplayName string  `json:"display_name"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Country     string  `json:"country"`
	City        string  `json:"city"`
}

func (t *GeocodeTool) Execute(ctx context.Context, input json.RawMessage, out chan<- string) ToolResult {
	var args geocodeArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolResult{Error: &ToolError{Code: "bad_input", Message: "invalid geocode args: " + err.Error()}}
	}

	out <- fmt.Sprintf("geocoding: %s\n", args.Query)

	apiURL := "https://nominatim.openstreetmap.org/search?q=" +
		url.QueryEscape(args.Query) + "&format=json&limit=1&addressdetails=1"

	var result ToolResult
	err := withRetry(ctx, 3, func() error {
		reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, apiURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", t.userAgent)

		resp, err := t.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return &httpError{statusCode: resp.StatusCode, message: fmt.Sprintf("nominatim returned %d", resp.StatusCode)}
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		var raw []struct {
			DisplayName string `json:"display_name"`
			Lat         string `json:"lat"`
			Lon         string `json:"lon"`
			Address     struct {
				Country string `json:"country"`
				City    string `json:"city"`
				Town    string `json:"town"`
				Village string `json:"village"`
			} `json:"address"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return err
		}
		if len(raw) == 0 {
			result = ToolResult{Error: &ToolError{Code: "not_found", Message: "no geocoding result for: " + args.Query}}
			return nil
		}

		var lat, lon float64
		fmt.Sscanf(raw[0].Lat, "%f", &lat)
		fmt.Sscanf(raw[0].Lon, "%f", &lon)

		city := raw[0].Address.City
		if city == "" {
			city = raw[0].Address.Town
		}
		if city == "" {
			city = raw[0].Address.Village
		}

		result = ToolResult{Data: geocodeResult{
			DisplayName: raw[0].DisplayName,
			Latitude:    lat,
			Longitude:   lon,
			Country:     raw[0].Address.Country,
			City:        city,
		}}
		return nil
	})

	if err != nil {
		return ToolResult{
			Error: &ToolError{Code: "unavailable", Message: "geocoding unavailable: " + err.Error(), Retryable: true},
		}
	}
	return result
}
