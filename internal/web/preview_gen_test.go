package web

import (
	"os"
	"testing"
	"time"

	"github.com/edvart/dota-inhouse/internal/coordinator"
)

// TestGeneratePreview is a throwaway visual check, skipped unless PREVIEW_OUT
// is set. Remove once the look is agreed.
func TestGeneratePreview(t *testing.T) {
	out := os.Getenv("PREVIEW_OUT")
	if out == "" {
		t.Skip("set PREVIEW_OUT to render a preview")
	}

	startedAt := time.Now().Add(-31 * time.Minute)
	match := &coordinator.Match{
		ID: "m", State: coordinator.MatchStateInProgress,
		DotaMatchID: 8995927103, GameStartedAt: &startedAt,
		LiveGameState: 5, LiveGameTime: 1456,
		Radiant: []coordinator.Player{
			{SteamID: "1", Name: "12b\u00e5ter"}, {SteamID: "2", Name: "Synnez"},
			{SteamID: "3", Name: "Don't Startle Me"}, {SteamID: "4", Name: "Haakki"},
			{SteamID: "5", Name: "Unregistered Player"},
		},
		Dire: []coordinator.Player{
			{SteamID: "6", Name: "endo"}, {SteamID: "7", Name: "Hix"},
			{SteamID: "8", Name: "WanXx"}, {SteamID: "9", Name: "Nintendo.6833"},
			{SteamID: "10", Name: "Sulyvahn"},
		},
		Heroes: map[string]int32{
			"1": 74, "2": 21, "3": 112, "4": 29, "5": 68,
			"6": 126, "7": 75, "8": 67, "9": 43, "10": 135,
		},
		HeroLevels: map[string]int32{
			"1": 21, "2": 18, "3": 14, "4": 17, "5": 13,
			"6": 20, "7": 15, "8": 22, "9": 18, "10": 16,
		},
		Stats: map[string]coordinator.PlayerStats{
			"1":  {Kills: 11, Deaths: 2, Assists: 9, NetWorth: 21840},
			"2":  {Kills: 6, Deaths: 4, Assists: 12, NetWorth: 14320},
			"3":  {Kills: 1, Deaths: 6, Assists: 18, NetWorth: 7210},
			"4":  {Kills: 3, Deaths: 5, Assists: 15, NetWorth: 11650},
			"5":  {Kills: 2, Deaths: 7, Assists: 14, NetWorth: 6480},
			"6":  {Kills: 9, Deaths: 5, Assists: 7, NetWorth: 18900},
			"7":  {Kills: 2, Deaths: 6, Assists: 13, NetWorth: 9870},
			"8":  {Kills: 8, Deaths: 3, Assists: 6, NetWorth: 26450},
			"9":  {Kills: 4, Deaths: 6, Assists: 10, NetWorth: 12080},
			"10": {Kills: 0, Deaths: 3, Assists: 16, NetWorth: 850},
		},
	}

	css, err := os.ReadFile("../../web/static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	panel := renderActiveMatchesPanel(t, []*coordinator.Match{match})

	page := `<!DOCTYPE html><html><head><meta charset="utf-8"><style>` + string(css) +
		`</style></head><body><div class="container" style="padding:1.5rem">` + panel + `</div></body></html>`

	if err := os.WriteFile(out, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", out)
}
