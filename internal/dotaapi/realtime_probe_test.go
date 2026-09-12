package dotaapi

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// Throwaway: validates the decoder against a live server.
// Run with PROBE_SERVER_ID=<id> STEAM_API_KEY=<key> go test ./internal/dotaapi -run TestLiveDecode -v
func TestLiveDecode(t *testing.T) {
	key := os.Getenv("STEAM_API_KEY")
	raw := os.Getenv("PROBE_SERVER_ID")
	if key == "" || raw == "" {
		t.Skip("STEAM_API_KEY and PROBE_SERVER_ID required")
	}
	serverID, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		t.Fatalf("bad PROBE_SERVER_ID: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	stats, err := NewRealtimeClient(key).GetRealtimeStats(ctx, serverID)
	if err != nil {
		t.Fatalf("GetRealtimeStats: %v", err)
	}

	m := stats.Match
	t.Logf("match_id=%d game_state=%d game_time=%d mode=%d lobby_type=%d league=%d",
		m.MatchID, m.GameState, m.GameTime, m.GameMode, m.LobbyType, m.LeagueID)
	t.Logf("picks=%v bans=%v", m.Picks, m.Bans)
	t.Logf("teams=%d picked=%d heroes=%v", len(stats.Teams), stats.PickedCount(), stats.HeroesByAccountID())

	if m.MatchID == 0 {
		t.Error("match_id decoded as 0 — check json tags")
	}
	if len(stats.Teams) != 2 {
		t.Errorf("expected 2 teams, got %d", len(stats.Teams))
	}
	// In a draft mode every heroid stays 0 until the draft resolves, so an
	// all-zero hero map is valid as long as the draft itself decoded.
	if stats.PickedCount() == 0 && len(m.Picks) == 0 && len(m.Bans) == 0 {
		t.Error("neither heroes nor draft decoded — check json tags")
	}
	for _, team := range stats.Teams {
		if team.TeamNumber == 0 {
			t.Error("team_number decoded as 0 — check json tags")
		}
		for _, p := range team.Players {
			if p.AccountID == 0 || p.Name == "" {
				t.Errorf("player decoded badly: %+v", p)
			}
		}
	}
}
