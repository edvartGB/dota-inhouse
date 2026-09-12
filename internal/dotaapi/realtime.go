package dotaapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const realtimeStatsURL = "https://api.steampowered.com/IDOTA2MatchStats_570/GetRealtimeStats/v1/"

var (
	// ErrRealtimeUnavailable means the game server is not serving realtime
	// stats. Valve signals this with HTTP 400, including for servers that have
	// been deallocated after a match ends.
	ErrRealtimeUnavailable = errors.New("realtime stats unavailable for server")

	// ErrRealtimeEmpty means the API returned a well-formed but empty payload.
	// This happens intermittently and should be retried rather than treated as
	// the game having ended.
	ErrRealtimeEmpty = errors.New("realtime stats response was empty")
)

// RealtimeClient queries Valve's live match stats endpoint. It needs a Steam
// Web API key; the endpoint returns 403 without one.
type RealtimeClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewRealtimeClient(apiKey string) *RealtimeClient {
	return &RealtimeClient{
		apiKey:  apiKey,
		baseURL: realtimeStatsURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// flexUint64 accepts a JSON number or a quoted number. Valve returns 64-bit
// ids either way depending on the field and the day.
type flexUint64 uint64

func (f *flexUint64) UnmarshalJSON(data []byte) error {
	text := strings.Trim(string(data), `"`)
	if text == "" || text == "null" {
		*f = 0
		return nil
	}
	value, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return fmt.Errorf("not a uint64: %q", text)
	}
	*f = flexUint64(value)
	return nil
}

// PickBan is one entry of the draft. Team is the realtime team number
// (2 = Radiant, 3 = Dire), matching RealtimeTeam.TeamNumber.
type PickBan struct {
	Hero int32  `json:"hero"`
	Team uint32 `json:"team"`
}

type RealtimeMatch struct {
	MatchID   flexUint64 `json:"match_id"`
	GameTime  int32      `json:"game_time"`
	GameState uint32     `json:"game_state"`
	GameMode  uint32     `json:"game_mode"`
	LeagueID  uint32     `json:"league_id"`
	LobbyType uint32     `json:"lobby_type"`
	Picks     []PickBan  `json:"picks"`
	Bans      []PickBan  `json:"bans"`
}

type RealtimePlayer struct {
	AccountID uint32 `json:"accountid"`
	Name      string `json:"name"`
	Team      uint32 `json:"team"`
	HeroID    int32  `json:"heroid"`
	Level     uint32 `json:"level"`
}

type RealtimeTeam struct {
	TeamNumber uint32           `json:"team_number"`
	Players    []RealtimePlayer `json:"players"`
}

type RealtimeStats struct {
	Match RealtimeMatch  `json:"match"`
	Teams []RealtimeTeam `json:"teams"`
}

// GetRealtimeStats fetches live state for a game server. serverSteamID is the
// anonymous gameserver SteamID64 that hosts the match.
func (c *RealtimeClient) GetRealtimeStats(ctx context.Context, serverSteamID uint64) (*RealtimeStats, error) {
	if c.apiKey == "" {
		return nil, errors.New("no Steam API key configured")
	}

	// Both values are typed (a string secret and a uint64), and are escaped by
	// url.Values, so nothing user-controlled reaches the request.
	query := url.Values{}
	query.Set("key", c.apiKey)
	query.Set("server_steam_id", strconv.FormatUint(serverSteamID, 10))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// The error from Do wraps the request URL, which carries the API key,
		// so report the status only.
		return nil, fmt.Errorf("realtime stats request failed for server %d", serverSteamID)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		return nil, ErrRealtimeUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("realtime stats returned status %d (%s)", resp.StatusCode, resp.Status)
	}

	var stats RealtimeStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, fmt.Errorf("failed to decode realtime stats: %w", err)
	}

	if stats.Match.MatchID == 0 && len(stats.Teams) == 0 {
		return nil, ErrRealtimeEmpty
	}

	return &stats, nil
}

// HeroesByAccountID flattens both teams into account ID to hero ID. A hero ID
// of 0 means that player has not picked yet.
func (s *RealtimeStats) HeroesByAccountID() map[uint32]int32 {
	heroes := make(map[uint32]int32)
	for _, team := range s.Teams {
		for _, player := range team.Players {
			heroes[player.AccountID] = player.HeroID
		}
	}
	return heroes
}

// PickedCount returns how many players have a hero assigned.
func (s *RealtimeStats) PickedCount() int {
	picked := 0
	for _, hero := range s.HeroesByAccountID() {
		if hero != 0 {
			picked++
		}
	}
	return picked
}
