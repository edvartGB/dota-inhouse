package coordinator

import (
	"context"
	"fmt"
	"testing"
)

func TestSelectCaptainsPicksTwoHighestPriorities(t *testing.T) {
	players := []Player{
		{SteamID: "a", CaptainPriority: 3},
		{SteamID: "b", CaptainPriority: 9},
		{SteamID: "c", CaptainPriority: 1},
		{SteamID: "d", CaptainPriority: 7},
		{SteamID: "e", CaptainPriority: 5},
	}
	for i := 0; i < 50; i++ {
		got := selectCaptains(players)
		if got[0].SteamID != "b" || got[1].SteamID != "d" {
			t.Fatalf("selectCaptains = %s,%s want b,d", got[0].SteamID, got[1].SteamID)
		}
	}
}

func TestSelectCaptainsBreaksTiesRandomlyWithinPriority(t *testing.T) {
	players := []Player{
		{SteamID: "low", CaptainPriority: 2},
		{SteamID: "x", CaptainPriority: 8},
		{SteamID: "y", CaptainPriority: 8},
		{SteamID: "z", CaptainPriority: 8},
		{SteamID: "mid", CaptainPriority: 5},
	}
	seen := map[string]bool{}
	for i := 0; i < 300; i++ {
		got := selectCaptains(players)
		for _, c := range got {
			if c.CaptainPriority != 8 {
				t.Fatalf("selected lower priority captain %s", c.SteamID)
			}
			seen[c.SteamID] = true
		}
		if got[0].SteamID == got[1].SteamID {
			t.Fatalf("same captain twice")
		}
	}
	if len(seen) != 3 {
		t.Errorf("tied captains selected = %v, want all of x,y,z over many runs", seen)
	}
}

func TestSelectCaptainsTooFewPlayers(t *testing.T) {
	if got := selectCaptains([]Player{{SteamID: "solo"}}); got[0].SteamID != "" || got[1].SteamID != "" {
		t.Errorf("selectCaptains with one player = %+v", got)
	}
}

func TestAcceptTimeoutRequeuesAcceptedPlayers(t *testing.T) {
	c := New()
	events := c.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	for i := 0; i < MaxPlayers+1; i++ {
		resp := make(chan error, 1)
		c.Send(JoinQueue{Player: Player{SteamID: fmt.Sprintf("p%02d", i), Name: fmt.Sprintf("P%02d", i), CaptainPriority: 1}, Response: resp})
		if err := <-resp; err != nil {
			t.Fatal(err)
		}
	}
	_, matches, _, _ := c.GetState()
	var match *Match
	for _, m := range matches {
		match = m
	}
	for _, p := range match.Players[:6] {
		resp := make(chan error, 1)
		c.Send(AcceptMatch{PlayerID: p.SteamID, MatchID: match.ID, Response: resp})
		if err := <-resp; err != nil {
			t.Fatal(err)
		}
	}
	for len(events) > 0 {
		<-events
	}

	c.Send(MatchAcceptTimeout{MatchID: match.ID})
	queue, matches, _, _ := c.GetState()

	if len(matches) != 0 {
		t.Errorf("matches after timeout = %d", len(matches))
	}
	var ids []string
	for _, p := range queue {
		ids = append(ids, p.SteamID)
	}
	want := []string{"p00", "p01", "p02", "p03", "p04", "p05", "p10"}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Errorf("queue after timeout = %v, want %v", ids, want)
	}

	var cancelled *MatchCancelled
	for len(events) > 0 {
		if ev, ok := (<-events).(MatchCancelled); ok {
			cancelled = &ev
		}
	}
	if cancelled == nil {
		t.Fatal("no MatchCancelled event")
	}
	if len(cancelled.FailedPlayers) != 4 || cancelled.FailedPlayers[0].SteamID != "p06" {
		t.Errorf("failed players = %+v", cancelled.FailedPlayers)
	}
}
