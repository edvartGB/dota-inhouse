package dotaapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetRealtimeStatsDecodesPlayerStats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"match": {"match_id": "8995927103", "game_time": 1456, "game_state": 5},
			"teams": [{
				"team_number": 2,
				"players": [{
					"accountid": 307286778, "name": "12bater", "team": 2, "heroid": 74,
					"level": 18, "kill_count": 11, "death_count": 2, "assists_count": 9,
					"net_worth": 29921
				}]
			}]
		}`))
	}))
	defer server.Close()

	client := NewRealtimeClient("test-key")
	client.baseURL = server.URL

	stats, err := client.GetRealtimeStats(context.Background(), 90292652787592203)
	if err != nil {
		t.Fatalf("GetRealtimeStats: %v", err)
	}

	player := stats.Teams[0].Players[0]
	if player.KillCount != 11 || player.DeathCount != 2 || player.AssistsCount != 9 {
		t.Errorf("K/D/A = %d/%d/%d, want 11/2/9", player.KillCount, player.DeathCount, player.AssistsCount)
	}
	if player.NetWorth != 29921 {
		t.Errorf("NetWorth = %d, want 29921", player.NetWorth)
	}
}
