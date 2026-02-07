package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	agk "github.com/agenticgokit/agenticgokit/v1beta"
)

type flightSearchTool struct{}

var (
	accessToken    string
	tokenExpiresAt time.Time
	tokenMu        sync.Mutex
)

func init() {
	agk.RegisterInternalTool("flight_search", func() agk.Tool { return &flightSearchTool{} })
}

func (t *flightSearchTool) Name() string {
	return "flight_search"
}

func (t *flightSearchTool) Description() string {
	return "Search for flights using origin, destination, dates, and preferences (Amadeus API)."
}

func (t *flightSearchTool) Execute(ctx context.Context, args map[string]interface{}) (*agk.ToolResult, error) {
	query := buildQuery(args)
	results, source, err := searchFlights(ctx, args)
	if err != nil {
		return &agk.ToolResult{Success: false, Error: err.Error()}, err
	}

	payload := map[string]interface{}{
		"query":   query,
		"results": results,
		"source":  source,
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return &agk.ToolResult{Success: false, Error: err.Error()}, err
	}

	return &agk.ToolResult{Success: true, Content: string(jsonBytes)}, nil
}

func (t *flightSearchTool) JSONSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"origin": map[string]interface{}{
				"type":        "string",
				"description": "Origin IATA airport code",
			},
			"destination": map[string]interface{}{
				"type":        "string",
				"description": "Destination IATA airport code",
			},
			"depart_date": map[string]interface{}{
				"type":        "string",
				"description": "Departure date (YYYY-MM-DD)",
			},
			"return_date": map[string]interface{}{
				"type":        "string",
				"description": "Return date (YYYY-MM-DD) or empty for one-way",
			},
			"passengers": map[string]interface{}{
				"type":        "number",
				"description": "Number of passengers",
			},
			"cabin": map[string]interface{}{
				"type":        "string",
				"description": "Cabin class",
			},
			"max_price": map[string]interface{}{
				"type":        "number",
				"description": "Maximum price",
			},
			"currency": map[string]interface{}{
				"type":        "string",
				"description": "Currency code",
			},
		},
		"required": []string{"origin", "destination", "depart_date"},
	}
}

func buildQuery(args map[string]interface{}) string {
	origin := getString(args, "origin")
	destination := getString(args, "destination")
	departDate := getString(args, "depart_date")
	returnDate := getString(args, "return_date")
	passengers := getNumber(args, "passengers")
	cabin := getString(args, "cabin")
	maxPrice := getNumber(args, "max_price")
	currency := getString(args, "currency")

	parts := []string{fmt.Sprintf("%s → %s", origin, destination)}
	if departDate != "" {
		parts = append(parts, "depart "+departDate)
	}
	if returnDate != "" {
		parts = append(parts, "return "+returnDate)
	}
	if passengers > 0 {
		parts = append(parts, fmt.Sprintf("%d pax", int(passengers)))
	}
	if cabin != "" {
		parts = append(parts, strings.ToLower(cabin))
	}
	if maxPrice > 0 {
		parts = append(parts, fmt.Sprintf("max %0.0f %s", maxPrice, currency))
	}

	return strings.Join(parts, ", ")
}

func searchFlights(ctx context.Context, args map[string]interface{}) ([]map[string]interface{}, string, error) {
	clientID := os.Getenv("AMADEUS_CLIENT_ID")
	clientSecret := os.Getenv("AMADEUS_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return nil, "amadeus", fmt.Errorf("missing AMADEUS_CLIENT_ID or AMADEUS_CLIENT_SECRET")
	}

	baseURL := os.Getenv("AMADEUS_BASE_URL")
	if baseURL == "" {
		baseURL = "https://test.api.amadeus.com"
	}

	token, err := getAccessToken(ctx, baseURL, clientID, clientSecret)
	if err != nil {
		return nil, "amadeus", err
	}

	query := url.Values{}
	query.Set("originLocationCode", getString(args, "origin"))
	query.Set("destinationLocationCode", getString(args, "destination"))
	query.Set("departureDate", getString(args, "depart_date"))
	if returnDate := getString(args, "return_date"); returnDate != "" {
		query.Set("returnDate", returnDate)
	}
	adults := getNumber(args, "passengers")
	if adults <= 0 {
		adults = 1
	}
	query.Set("adults", fmt.Sprintf("%d", int(adults)))
	if cabin := strings.ToUpper(getString(args, "cabin")); cabin != "" {
		query.Set("travelClass", cabin)
	}
	if currency := getString(args, "currency"); currency != "" {
		query.Set("currencyCode", currency)
	}
	if maxPrice := getNumber(args, "max_price"); maxPrice > 0 {
		query.Set("maxPrice", fmt.Sprintf("%0.0f", maxPrice))
	}
	query.Set("nonStop", "false")

	endpoint := fmt.Sprintf("%s/v2/shopping/flight-offers?%s", baseURL, query.Encode())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "amadeus", err
	}
	request.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(request)
	if err != nil {
		return nil, "amadeus", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "amadeus", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "amadeus", fmt.Errorf("amadeus flight offers request failed: %s", resp.Status)
	}

	parsed, err := parseAmadeusOffers(body)
	if err != nil {
		return nil, "amadeus", err
	}

	return parsed, "amadeus", nil
}

func getAccessToken(ctx context.Context, baseURL, clientID, clientSecret string) (string, error) {
	tokenMu.Lock()
	defer tokenMu.Unlock()

	if accessToken != "" && time.Now().Before(tokenExpiresAt.Add(-30*time.Second)) {
		return accessToken, nil
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/security/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("amadeus token request failed: %s", resp.Status)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", err
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("amadeus token response missing access_token")
	}

	accessToken = tokenResp.AccessToken
	if tokenResp.ExpiresIn <= 0 {
		tokenExpiresAt = time.Now().Add(20 * time.Minute)
	} else {
		tokenExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	return accessToken, nil
}

func parseAmadeusOffers(body []byte) ([]map[string]interface{}, error) {
	var raw struct {
		Data []struct {
			Price struct {
				Total    string `json:"total"`
				Currency string `json:"currency"`
			} `json:"price"`
			Itineraries []struct {
				Duration string `json:"duration"`
				Segments []struct {
					CarrierCode string `json:"carrierCode"`
					Number      string `json:"number"`
					Departure   struct {
						IataCode string `json:"iataCode"`
						At       string `json:"at"`
					} `json:"departure"`
					Arrival struct {
						IataCode string `json:"iataCode"`
						At       string `json:"at"`
					} `json:"arrival"`
				} `json:"segments"`
			} `json:"itineraries"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	results := make([]map[string]interface{}, 0, len(raw.Data))
	for _, offer := range raw.Data {
		if len(offer.Itineraries) == 0 || len(offer.Itineraries[0].Segments) == 0 {
			continue
		}
		segments := offer.Itineraries[0].Segments
		first := segments[0]
		last := segments[len(segments)-1]
		flightNumber := strings.TrimSpace(first.CarrierCode + first.Number)
		departTime := timeFromISO(first.Departure.At)
		arriveTime := timeFromISO(last.Arrival.At)

		results = append(results, map[string]interface{}{
			"airline":       first.CarrierCode,
			"flight_number": flightNumber,
			"origin":        first.Departure.IataCode,
			"destination":   last.Arrival.IataCode,
			"depart_time":   departTime,
			"arrive_time":   arriveTime,
			"duration":      offer.Itineraries[0].Duration,
			"stops":         len(segments) - 1,
			"price":         offer.Price.Total,
			"currency":      offer.Price.Currency,
		})
	}

	return results, nil
}

func timeFromISO(value string) string {
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "T")
	if len(parts) == 2 {
		return parts[1]
	}
	return value
}

func getString(args map[string]interface{}, key string) string {
	if val, ok := args[key]; ok {
		switch v := val.(type) {
		case string:
			return strings.TrimSpace(v)
		default:
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

func getNumber(args map[string]interface{}, key string) float64 {
	if val, ok := args[key]; ok {
		switch v := val.(type) {
		case float64:
			return v
		case float32:
			return float64(v)
		case int:
			return float64(v)
		case int64:
			return float64(v)
		case json.Number:
			f, _ := v.Float64()
			return f
		case string:
			var num json.Number = json.Number(v)
			f, _ := num.Float64()
			return f
		}
	}
	return 0
}
