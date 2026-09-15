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
	}

	css, err := os.ReadFile("../../web/static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	panel := renderActiveMatchesPanel(t, []*coordinator.Match{match})

	page := `<!DOCTYPE html><html><head><meta charset="utf-8"><style>` + string(css) +
		`</style></head><body><div class="sidebar" style="width:340px;padding:1rem">` + panel + `</div></body></html>`

	if err := os.WriteFile(out, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", out)
}
