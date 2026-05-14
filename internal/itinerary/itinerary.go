// Package itinerary defines the typed output structure the agent produces.
package itinerary

// Activity is a single planned activity within a day.
type Activity struct {
	Time     string `json:"time"`
	Name     string `json:"name"`
	Notes    string `json:"notes,omitempty"`
	DeepLink string `json:"deep_link,omitempty"`
}

// Day is a single day of the itinerary.
type Day struct {
	Date        string     `json:"date"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	Activities  []Activity `json:"activities,omitempty"`
}

// Accommodation holds the chosen hotel with a Booking.com deep link.
type Accommodation struct {
	Name     string `json:"name"`
	DeepLink string `json:"deep_link,omitempty"`
}

// Flight represents one leg of travel.
type Flight struct {
	Direction string `json:"direction"` // "outbound" | "return"
	Airline   string `json:"airline"`
	DeepLink  string `json:"deep_link,omitempty"`
}

// Budget is a cost breakdown for the entire trip in USD.
type Budget struct {
	TotalUSD         float64 `json:"total_usd"`
	FlightsUSD       float64 `json:"flights_usd"`
	AccommodationUSD float64 `json:"accommodation_usd"`
	ActivitiesUSD    float64 `json:"activities_usd"`
	FoodUSD          float64 `json:"food_usd"`
	MiscUSD          float64 `json:"misc_usd"`
}

// Itinerary is the complete, structured output of a planning session.
type Itinerary struct {
	Destination   string        `json:"destination"`
	StartDate     string        `json:"start_date"`
	EndDate       string        `json:"end_date"`
	Summary       string        `json:"summary"`
	Days          []Day         `json:"days"`
	Accommodation Accommodation `json:"accommodation,omitempty"`
	Flights       []Flight      `json:"flights,omitempty"`
	Budget        Budget        `json:"budget,omitempty"`
	Notes         string        `json:"notes,omitempty"`
}
