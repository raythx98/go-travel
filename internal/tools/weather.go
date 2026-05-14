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

// forecastWindow is the maximum number of days ahead the Open-Meteo forecast API supports.
const forecastWindow = 16

// WeatherTool fetches weather forecasts from Open-Meteo (free, no key).
// For travel dates within the next 16 days it uses the live forecast API.
// For dates further out it falls back to the archive API using the equivalent
// dates from the previous year as a seasonal proxy.
type WeatherTool struct {
	client *http.Client
}

// NewWeatherTool returns a WeatherTool with a sensible default timeout.
func NewWeatherTool() *WeatherTool {
	return &WeatherTool{client: &http.Client{Timeout: 5 * time.Second}}
}

func (t *WeatherTool) Definition() llm.Tool {
	return llm.Tool{
		Name: "get_weather",
		Description: `Fetch weather forecast for a location over given travel dates.
Use when planning outdoor activities or when weather affects the itinerary.
Returns daily summaries including temperature range, precipitation, and conditions.
For dates more than 16 days ahead, returns historical climate averages from the same period last year.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"latitude":   map[string]any{"type": "number", "description": "Latitude of the location"},
				"longitude":  map[string]any{"type": "number", "description": "Longitude of the location"},
				"start_date": map[string]any{"type": "string", "description": "Start date YYYY-MM-DD"},
				"end_date":   map[string]any{"type": "string", "description": "End date YYYY-MM-DD"},
			},
			"required": []string{"latitude", "longitude", "start_date", "end_date"},
		},
	}
}

type weatherArgs struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	StartDate string  `json:"start_date"`
	EndDate   string  `json:"end_date"`
}

type weatherDay struct {
	Date         string  `json:"date"`
	MaxTempC     float64 `json:"max_temp_c"`
	MinTempC     float64 `json:"min_temp_c"`
	PrecipMM     float64 `json:"precip_mm"`
	WindspeedKmh float64 `json:"windspeed_kmh"`
	WeatherCode  int     `json:"weather_code"`
	Description  string  `json:"description"`
}

type weatherResult struct {
	Days       []weatherDay `json:"days"`
	Historical bool         `json:"historical,omitempty"` // true when data is from previous year
}

func (t *WeatherTool) Execute(ctx context.Context, input json.RawMessage, out chan<- string) ToolResult {
	var args weatherArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolResult{Error: &ToolError{Code: "bad_input", Message: "invalid weather args: " + err.Error()}}
	}

	start, err := time.Parse("2006-01-02", args.StartDate)
	if err != nil {
		return ToolResult{Error: &ToolError{Code: "bad_input", Message: "invalid start_date: " + err.Error()}}
	}

	// Determine whether to use the live forecast or the archive API.
	// Past dates and dates beyond the 16-day forecast window both require the archive API.
	// Only far-future dates need a -1 year shift as a seasonal climate proxy.
	daysAhead := int(time.Until(start).Hours() / 24)
	useArchive := daysAhead > forecastWindow || daysAhead < 0
	shiftDates := daysAhead > forecastWindow

	queryStart := args.StartDate
	queryEnd := args.EndDate
	if shiftDates {
		// Shift dates back one year to get seasonal climate data as a proxy.
		end, err := time.Parse("2006-01-02", args.EndDate)
		if err != nil {
			return ToolResult{Error: &ToolError{Code: "bad_input", Message: "invalid end_date: " + err.Error()}}
		}
		queryStart = start.AddDate(-1, 0, 0).Format("2006-01-02")
		queryEnd = end.AddDate(-1, 0, 0).Format("2006-01-02")
		out <- event.Encode(event.Token{Type: "token", Content: fmt.Sprintf("Fetching climate averages for %s (historical proxy from %s)\n", args.StartDate, queryStart)})
	} else {
		out <- event.Encode(event.Token{Type: "token", Content: fmt.Sprintf("Fetching weather forecast for %s\n", args.StartDate)})
	}

	apiURL := buildWeatherURL(args.Latitude, args.Longitude, queryStart, queryEnd, useArchive)

	var days []weatherDay
	fetchErr := withRetry(ctx, 3, func() error {
		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
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
			return &httpError{statusCode: resp.StatusCode, message: fmt.Sprintf("open-meteo returned %d", resp.StatusCode)}
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		var raw struct {
			Daily struct {
				Time        []string  `json:"time"`
				TempMax     []float64 `json:"temperature_2m_max"`
				TempMin     []float64 `json:"temperature_2m_min"`
				Precip      []float64 `json:"precipitation_sum"`
				Windspeed   []float64 `json:"windspeed_10m_max"`
				WeatherCode []int     `json:"weathercode"`
			} `json:"daily"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return err
		}

		days = make([]weatherDay, len(raw.Daily.Time))
		for i, date := range raw.Daily.Time {
			code := 0
			if i < len(raw.Daily.WeatherCode) {
				code = raw.Daily.WeatherCode[i]
			}
			// Remap archive dates back to the actual travel dates (only when shifted).
			label := date
			if shiftDates && i < len(raw.Daily.Time) {
				if d, err := time.Parse("2006-01-02", date); err == nil {
					label = d.AddDate(1, 0, 0).Format("2006-01-02")
				}
			}
			days[i] = weatherDay{
				Date:         label,
				MaxTempC:     safeIndex(raw.Daily.TempMax, i),
				MinTempC:     safeIndex(raw.Daily.TempMin, i),
				PrecipMM:     safeIndex(raw.Daily.Precip, i),
				WindspeedKmh: safeIndex(raw.Daily.Windspeed, i),
				WeatherCode:  code,
				Description:  wmoDescription(code),
			}
		}
		return nil
	})

	if fetchErr != nil {
		return ToolResult{
			Error:    &ToolError{Code: "unavailable", Message: "weather forecast unavailable: " + fetchErr.Error(), Retryable: true},
			Degraded: true,
		}
	}
	return ToolResult{Data: weatherResult{Days: days, Historical: useArchive}}
}

func buildWeatherURL(lat, lon float64, startDate, endDate string, historical bool) string {
	base := "https://api.open-meteo.com/v1/forecast"
	if historical {
		base = "https://archive-api.open-meteo.com/v1/archive"
	}
	return fmt.Sprintf(
		"%s?latitude=%.4f&longitude=%.4f"+
			"&daily=temperature_2m_max,temperature_2m_min,precipitation_sum,windspeed_10m_max,weathercode"+
			"&start_date=%s&end_date=%s&timezone=auto",
		base, lat, lon, url.QueryEscape(startDate), url.QueryEscape(endDate),
	)
}

func safeIndex(s []float64, i int) float64 {
	if i < len(s) {
		return s[i]
	}
	return 0
}

// wmoDescription maps WMO weather interpretation codes to human-readable strings.
func wmoDescription(code int) string {
	switch {
	case code == 0:
		return "Clear sky"
	case code == 1, code == 2, code == 3:
		return "Partly cloudy"
	case code >= 45 && code <= 48:
		return "Foggy"
	case code >= 51 && code <= 57:
		return "Drizzle"
	case code >= 61 && code <= 67:
		return "Rain"
	case code >= 71 && code <= 77:
		return "Snow"
	case code >= 80 && code <= 82:
		return "Rain showers"
	case code >= 95 && code <= 99:
		return "Thunderstorm"
	default:
		return "Unknown"
	}
}
