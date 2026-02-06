package dotaapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	baseURL = "https://api.steampowered.com/IDOTA2Match_570/GetMatchDetails/v1"
)

// Client handles Dota 2 Web API requests.
type Client struct {
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new Dota 2 API client.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// MatchDetails contains the relevant match information from the API.
type MatchDetails struct {
	MatchID       uint64 `json:"match_id"`
	RadiantWin    bool   `json:"radiant_win"`
	Duration      int    `json:"duration"` // Duration in seconds
	StartTime     int64  `json:"start_time"`
	GameMode      int    `json:"game_mode"`
	RadiantScore  int    `json:"radiant_score"`
	DireScore     int    `json:"dire_score"`
}

// apiResponse wraps the API response.
type apiResponse struct {
	Result MatchDetails `json:"result"`
}

// GetMatchDetails fetches match details from the Dota 2 API.
func (c *Client) GetMatchDetails(ctx context.Context, matchID uint64) (*MatchDetails, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("no API key configured")
	}

	url := fmt.Sprintf("%s?key=%s&match_id=%d", baseURL, c.apiKey, matchID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch match details: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var result apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Check if match was found (match_id will be 0 if not found)
	if result.Result.MatchID == 0 {
		return nil, fmt.Errorf("match not found")
	}

	return &result.Result, nil
}

// Winner returns "radiant" or "dire" based on the match result.
func (m *MatchDetails) Winner() string {
	if m.RadiantWin {
		return "radiant"
	}
	return "dire"
}

// DurationFormatted returns the duration as a formatted string (e.g., "45:32").
func (m *MatchDetails) DurationFormatted() string {
	minutes := m.Duration / 60
	seconds := m.Duration % 60
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}
