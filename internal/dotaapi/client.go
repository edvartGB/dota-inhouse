package dotaapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	baseURL = "https://api.opendota.com/api/matches"
)

// Client handles OpenDota API requests.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new OpenDota API client.
func NewClient() *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// MatchDetails contains the relevant match information from OpenDota.
type MatchDetails struct {
	MatchID      uint64 `json:"match_id"`
	RadiantWin   bool   `json:"radiant_win"`
	Duration     int    `json:"duration"` // Duration in seconds
	StartTime    int64  `json:"start_time"`
	GameMode     int    `json:"game_mode"`
	RadiantScore int    `json:"radiant_score"`
	DireScore    int    `json:"dire_score"`
}

// GetMatchDetails fetches match details from OpenDota.
func (c *Client) GetMatchDetails(ctx context.Context, matchID uint64) (*MatchDetails, error) {
	url := fmt.Sprintf("%s/%d", strings.TrimRight(c.baseURL, "/"), matchID)

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
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		body := strings.TrimSpace(string(bodyBytes))
		if body == "" {
			return nil, fmt.Errorf("API returned status %d (%s)", resp.StatusCode, resp.Status)
		}
		return nil, fmt.Errorf("API returned status %d (%s): %q", resp.StatusCode, resp.Status, body)
	}

	var result MatchDetails
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Check if match was found (match_id will be 0 if not found).
	if result.MatchID == 0 {
		return nil, fmt.Errorf("match not found")
	}

	return &result, nil
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
