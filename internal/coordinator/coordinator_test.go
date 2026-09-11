package coordinator

import "testing"

func TestGetSideChooserIndexUsesLowerPriorityCaptain(t *testing.T) {
	captains := [2]Player{
		{SteamID: "higher", CaptainPriority: 10},
		{SteamID: "lower", CaptainPriority: 3},
	}

	if got := getSideChooserIndex(captains); got != 1 {
		t.Fatalf("getSideChooserIndex() = %d, want 1", got)
	}
}

func TestArrangeCaptainsForSideChoice(t *testing.T) {
	captains := [2]Player{
		{SteamID: "a", Name: "Alpha"},
		{SteamID: "b", Name: "Beta"},
	}

	radiant := arrangeCaptainsForSideChoice(captains, 1, "radiant")
	if radiant[0].SteamID != "b" || radiant[1].SteamID != "a" {
		t.Fatalf("radiant choice = [%s %s], want [b a]", radiant[0].SteamID, radiant[1].SteamID)
	}

	dire := arrangeCaptainsForSideChoice(captains, 1, "dire")
	if dire[0].SteamID != "a" || dire[1].SteamID != "b" {
		t.Fatalf("dire choice = [%s %s], want [a b]", dire[0].SteamID, dire[1].SteamID)
	}
}

func TestHandleChooseSideStartsDraftWithChosenSide(t *testing.T) {
	c := New()
	captains := [2]Player{
		{SteamID: "high", Name: "High", CaptainPriority: 10},
		{SteamID: "low", Name: "Low", CaptainPriority: 1},
	}
	match := &Match{
		ID:               "match",
		State:            MatchStateChoosingSide,
		Players:          []Player{captains[0], captains[1], {SteamID: "p3"}, {SteamID: "p4"}},
		Captains:         captains,
		SideChooserIndex: 1,
	}
	c.state.Matches[match.ID] = match

	err := c.handleChooseSide(ChooseSide{
		MatchID:   match.ID,
		CaptainID: "low",
		Side:      "dire",
	})
	if err != nil {
		t.Fatalf("handleChooseSide() error = %v", err)
	}

	if match.State != MatchStateDrafting {
		t.Fatalf("match.State = %v, want %v", match.State, MatchStateDrafting)
	}
	if match.Radiant[0].SteamID != "high" {
		t.Fatalf("radiant captain = %s, want high", match.Radiant[0].SteamID)
	}
	if match.Dire[0].SteamID != "low" {
		t.Fatalf("dire captain = %s, want low", match.Dire[0].SteamID)
	}
}
