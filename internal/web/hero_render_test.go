package web

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/edvart/dota-inhouse/internal/coordinator"
)

// renderActiveMatchesPanel renders the sidebar panel the SSE hub pushes.
func renderActiveMatchesPanel(t *testing.T, matches []*coordinator.Match) string {
	t.Helper()

	tmpl, err := LoadTemplatesFromDir("../../web/templates")
	if err != nil {
		t.Fatalf("loading templates: %v", err)
	}

	data := struct{ Matches []*coordinator.Match }{Matches: matches}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "active-matches", data); err != nil {
		t.Fatalf("rendering active-matches: %v", err)
	}
	return buf.String()
}

// inProgressMatch mirrors the real lineup from match 8995927103.
func inProgressMatch(heroes map[string]int32) *coordinator.Match {
	startedAt := time.Now()
	return &coordinator.Match{
		ID:            "test-match",
		State:         coordinator.MatchStateInProgress,
		DotaMatchID:   8995927103,
		GameStartedAt: &startedAt,
		Radiant: []coordinator.Player{
			{SteamID: "76561198267552506", Name: "12bater"}, // Invoker, 74
			{SteamID: "76561197988574788", Name: "Synnez"},  // Windranger, 21
		},
		Dire: []coordinator.Player{
			{SteamID: "76561197980833499", Name: "endo"}, // Void Spirit, 126
		},
		Heroes: heroes,
	}
}

func TestActiveMatchesRendersHeroPortraits(t *testing.T) {
	html := renderActiveMatchesPanel(t, []*coordinator.Match{inProgressMatch(map[string]int32{
		"76561198267552506": 74,  // Invoker
		"76561197988574788": 21,  // Windranger
		"76561197980833499": 126, // Void Spirit
	})})

	// Windranger's asset slug is "windrunner", so a passing assertion here also
	// proves the template is going through the generated slug table rather than
	// deriving a URL from the display name.
	for _, want := range []string{
		"heroes/invoker.png",
		"heroes/windrunner.png",
		"heroes/void_spirit.png",
		`alt="Windranger"`,
		`class="hero-portrait"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered panel missing %q", want)
		}
	}

	if got := strings.Count(html, `class="hero-portrait"`); got != 3 {
		t.Errorf("got %d portraits, want 3", got)
	}

	// Names must survive alongside the art.
	for _, name := range []string{"12bater", "Synnez", "endo"} {
		if !strings.Contains(html, name) {
			t.Errorf("rendered panel missing player %q", name)
		}
	}
}

func TestActiveMatchesWithoutHeroesRendersNoPortraits(t *testing.T) {
	// A match mid-draft has no hero data at all. The panel must still render
	// the rosters, and must not emit a broken image.
	html := renderActiveMatchesPanel(t, []*coordinator.Match{inProgressMatch(nil)})

	if strings.Contains(html, "hero-portrait") {
		t.Error("rendered a portrait for a match with no hero data")
	}
	if !strings.Contains(html, "Synnez") {
		t.Error("player names should render even without hero data")
	}
}

func TestActiveMatchesIgnoresUnknownHeroID(t *testing.T) {
	// Valve adds heroes between our codegen runs, and id 0 means "not picked".
	// Neither may produce an <img> with an empty src.
	html := renderActiveMatchesPanel(t, []*coordinator.Match{inProgressMatch(map[string]int32{
		"76561198267552506": 0,
		"76561197988574788": 99999,
		"76561197980833499": 126,
	})})

	if got := strings.Count(html, `class="hero-portrait"`); got != 1 {
		t.Errorf("got %d portraits, want 1 (only the known hero)", got)
	}
	if strings.Contains(html, `src=""`) {
		t.Error("emitted an image with an empty src")
	}
}
