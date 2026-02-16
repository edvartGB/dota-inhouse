package coordinator

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func testPlayers(n int) []Player {
	players := make([]Player, n)
	for i := 0; i < n; i++ {
		players[i] = Player{
			SteamID:         fmt.Sprintf("p%d", i+1),
			Name:            fmt.Sprintf("Player %d", i+1),
			CaptainPriority: i + 1,
		}
	}
	return players
}

func withMaxPlayers(t *testing.T, n int) {
	t.Helper()
	old := MaxPlayers
	MaxPlayers = n
	t.Cleanup(func() {
		MaxPlayers = old
	})
}

func singleMatch(t *testing.T, c *Coordinator) (string, *Match) {
	t.Helper()
	if len(c.state.Matches) != 1 {
		t.Fatalf("expected exactly 1 match, got %d", len(c.state.Matches))
	}
	for id, m := range c.state.Matches {
		return id, m
	}
	t.Fatal("unreachable")
	return "", nil
}

func waitForEvent(t *testing.T, ch <-chan Event, predicate func(Event) bool) Event {
	t.Helper()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case <-timeout.C:
			t.Fatal("timed out waiting for event")
		case e := <-ch:
			if predicate(e) {
				return e
			}
		}
	}
}

func TestHandleJoinAndLeaveQueue(t *testing.T) {
	withMaxPlayers(t, 10)
	c := New()
	c.state.QueueOpen = true
	players := testPlayers(2)

	if err := c.handleJoinQueue(JoinQueue{Player: players[0]}); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	if err := c.handleJoinQueue(JoinQueue{Player: players[0]}); err == nil {
		t.Fatal("expected duplicate join to fail")
	}
	if err := c.handleJoinQueue(JoinQueue{Player: players[1]}); err != nil {
		t.Fatalf("second join failed: %v", err)
	}

	if got := len(c.state.Queue); got != 2 {
		t.Fatalf("expected queue length 2, got %d", got)
	}

	if err := c.handleLeaveQueue(LeaveQueue{PlayerID: players[0].SteamID}); err != nil {
		t.Fatalf("leave failed: %v", err)
	}
	if got := len(c.state.Queue); got != 1 {
		t.Fatalf("expected queue length 1 after leave, got %d", got)
	}
	if err := c.handleLeaveQueue(LeaveQueue{PlayerID: players[0].SteamID}); err == nil {
		t.Fatal("expected leaving non-queued player to fail")
	}
}

func TestJoinQueueStartsMatchAcceptance(t *testing.T) {
	withMaxPlayers(t, 2)
	c := New()
	c.state.QueueOpen = true
	events := c.Subscribe()
	players := testPlayers(2)

	if err := c.handleJoinQueue(JoinQueue{Player: players[0]}); err != nil {
		t.Fatalf("first join failed: %v", err)
	}
	if err := c.handleJoinQueue(JoinQueue{Player: players[1]}); err != nil {
		t.Fatalf("second join failed: %v", err)
	}

	_, match := singleMatch(t, c)
	if match.State != MatchStateAccepting {
		t.Fatalf("expected accepting state, got %v", match.State)
	}
	if len(c.state.Queue) != 0 {
		t.Fatalf("expected queue to be empty after match start, got %d", len(c.state.Queue))
	}

	e := waitForEvent(t, events, func(ev Event) bool {
		_, ok := ev.(MatchAcceptStarted)
		return ok
	})
	started := e.(MatchAcceptStarted)
	if started.MatchID == "" {
		t.Fatal("expected non-empty match ID")
	}
	if len(started.Players) != 2 {
		t.Fatalf("expected 2 players in acceptance event, got %d", len(started.Players))
	}
}

func TestAcceptAllMovesToDraft(t *testing.T) {
	withMaxPlayers(t, 4)
	c := New()
	c.state.QueueOpen = true
	players := testPlayers(4)

	for _, p := range players {
		if err := c.handleJoinQueue(JoinQueue{Player: p}); err != nil {
			t.Fatalf("join failed: %v", err)
		}
	}

	matchID, match := singleMatch(t, c)
	for _, p := range match.Players {
		if err := c.handleAcceptMatch(AcceptMatch{PlayerID: p.SteamID, MatchID: matchID}); err != nil {
			t.Fatalf("accept failed for %s: %v", p.SteamID, err)
		}
	}

	if match.State != MatchStateDrafting {
		t.Fatalf("expected drafting state, got %v", match.State)
	}
	if len(match.Captains) != 2 {
		t.Fatalf("expected 2 captains, got %d", len(match.Captains))
	}
	if len(match.Radiant) != 1 || len(match.Dire) != 1 {
		t.Fatalf("expected captains assigned to teams, got radiant=%d dire=%d", len(match.Radiant), len(match.Dire))
	}
	if len(match.AvailablePlayers) != 2 {
		t.Fatalf("expected 2 available players after captain selection, got %d", len(match.AvailablePlayers))
	}
}

func TestMatchAcceptTimeoutRequeuesAcceptedPlayers(t *testing.T) {
	withMaxPlayers(t, 4)
	c := New()
	players := testPlayers(4)

	c.state.Queue = []Player{{SteamID: "q1", Name: "Queued"}}
	c.state.Matches["m1"] = &Match{
		ID:             "m1",
		State:          MatchStateAccepting,
		Players:        players,
		AcceptedPlayers: map[string]bool{
			players[0].SteamID: true,
			players[1].SteamID: true,
		},
	}

	c.handleMatchAcceptTimeout(MatchAcceptTimeout{MatchID: "m1"})

	if _, ok := c.state.Matches["m1"]; ok {
		t.Fatal("expected timed out match to be removed")
	}
	if got := len(c.state.Queue); got != 3 {
		t.Fatalf("expected 3 players in queue, got %d", got)
	}
	if c.state.Queue[0].SteamID != players[0].SteamID || c.state.Queue[1].SteamID != players[1].SteamID {
		t.Fatalf("expected accepted players requeued first, got queue=%v", c.state.Queue)
	}
}

func TestPickPlayerAutoAssignsLastAndCompletesDraft(t *testing.T) {
	withMaxPlayers(t, 4)
	c := New()
	events := c.Subscribe()
	players := testPlayers(4)

	c.state.Matches["m1"] = &Match{
		ID:               "m1",
		State:            MatchStateDrafting,
		Players:          players,
		Captains:         [2]Player{players[0], players[1]},
		Radiant:          []Player{players[0]},
		Dire:             []Player{players[1]},
		AvailablePlayers: []Player{players[2], players[3]},
		CurrentPicker:    0,
		PickCount:        0,
	}

	err := c.handlePickPlayer(PickPlayer{
		CaptainID: players[0].SteamID,
		PickedID:  players[2].SteamID,
		MatchID:   "m1",
	})
	if err != nil {
		t.Fatalf("pick failed: %v", err)
	}

	match := c.state.Matches["m1"]
	if match.State != MatchStateWaitingForBot {
		t.Fatalf("expected waiting_for_bot, got %v", match.State)
	}
	if len(match.AvailablePlayers) != 0 {
		t.Fatalf("expected no available players after auto-assign, got %d", len(match.AvailablePlayers))
	}
	if len(match.Radiant) != 2 || len(match.Dire) != 2 {
		t.Fatalf("expected 2 players per side, got radiant=%d dire=%d", len(match.Radiant), len(match.Dire))
	}

	e := waitForEvent(t, events, func(ev Event) bool {
		_, ok := ev.(RequestBotLobby)
		return ok
	})
	req := e.(RequestBotLobby)
	if req.MatchID != "m1" {
		t.Fatalf("expected lobby request for m1, got %s", req.MatchID)
	}
}

func TestPickPlayerRejectsWrongCaptain(t *testing.T) {
	withMaxPlayers(t, 4)
	c := New()
	players := testPlayers(4)

	c.state.Matches["m1"] = &Match{
		ID:               "m1",
		State:            MatchStateDrafting,
		Players:          players,
		Captains:         [2]Player{players[0], players[1]},
		Radiant:          []Player{players[0]},
		Dire:             []Player{players[1]},
		AvailablePlayers: []Player{players[2], players[3]},
		CurrentPicker:    0,
	}

	err := c.handlePickPlayer(PickPlayer{
		CaptainID: players[1].SteamID,
		PickedID:  players[2].SteamID,
		MatchID:   "m1",
	})
	if err == nil {
		t.Fatal("expected wrong captain pick to fail")
	}
}

func TestDraftTimeoutCancelsAndRequeuesOthers(t *testing.T) {
	withMaxPlayers(t, 4)
	c := New()
	players := testPlayers(4)

	c.state.Queue = []Player{{SteamID: "q1", Name: "Queued"}}
	c.state.Matches["m1"] = &Match{
		ID:            "m1",
		State:         MatchStateDrafting,
		Players:       players,
		Captains:      [2]Player{players[0], players[1]},
		CurrentPicker: 1, // Dire captain fails
		PickCount:     2,
	}

	c.handleDraftPickTimeout(DraftPickTimeout{MatchID: "m1", PickNumber: 2})

	if _, ok := c.state.Matches["m1"]; ok {
		t.Fatal("expected match removed after draft timeout")
	}
	if got := len(c.state.Queue); got != 4 {
		t.Fatalf("expected 4 queued players (3 returned + 1 existing), got %d", got)
	}
	for _, p := range c.state.Queue {
		if p.SteamID == players[1].SteamID {
			t.Fatalf("failed captain %s should not be requeued", p.SteamID)
		}
	}
}

func TestLobbyTimeoutRequeuesOnlyCorrectJoiners(t *testing.T) {
	withMaxPlayers(t, 4)
	c := New()
	players := testPlayers(4)

	c.state.Queue = []Player{{SteamID: "q1", Name: "Queued"}}
	c.state.Matches["m1"] = &Match{
		ID:      "m1",
		State:   MatchStateWaitingForBot,
		Players: players,
	}

	c.handleBotLobbyTimeout(BotLobbyTimeout{
		MatchID:            "m1",
		PlayersJoinedRight: []string{players[2].SteamID},
	})

	if _, ok := c.state.Matches["m1"]; ok {
		t.Fatal("expected match removed after lobby timeout")
	}
	if got := len(c.state.Queue); got != 2 {
		t.Fatalf("expected only correctly joined player + existing queue, got %d", got)
	}
	if c.state.Queue[0].SteamID != players[2].SteamID {
		t.Fatalf("expected correctly joined player to be requeued first, got %s", c.state.Queue[0].SteamID)
	}
}

func TestAdminSetLobbySettingsValidation(t *testing.T) {
	c := New()

	if err := c.handleAdminSetLobbySettings(AdminSetLobbySettings{
		Settings: LobbySettings{GameMode: "invalid"},
	}); err == nil {
		t.Fatal("expected invalid game mode to fail")
	}

	if err := c.handleAdminSetLobbySettings(AdminSetLobbySettings{
		Settings: LobbySettings{GameMode: "cm"},
	}); err != nil {
		t.Fatalf("expected valid game mode, got error: %v", err)
	}
	if c.state.LobbySettings.GameMode != "cm" {
		t.Fatalf("expected game mode cm, got %s", c.state.LobbySettings.GameMode)
	}
}

func TestGetPickerForPickCount(t *testing.T) {
	tests := map[int]int{
		0: 0,
		1: 1,
		2: 1,
		3: 0,
		4: 0,
		5: 1,
		6: 1,
		7: 0,
		8: 0, // fallback branch
	}

	for pick, want := range tests {
		if got := getPickerForPickCount(pick); got != want {
			t.Fatalf("pick %d: expected picker %d, got %d", pick, want, got)
		}
	}
}

func TestSelectCaptainsUsesHighestPriority(t *testing.T) {
	players := []Player{
		{SteamID: "a", CaptainPriority: 1},
		{SteamID: "b", CaptainPriority: 5},
		{SteamID: "c", CaptainPriority: 3},
		{SteamID: "d", CaptainPriority: 4},
	}

	captains := selectCaptains(players)
	got := map[string]bool{
		captains[0].SteamID: true,
		captains[1].SteamID: true,
	}
	if !got["b"] || !got["d"] {
		t.Fatalf("expected top priority captains b and d, got %+v", captains)
	}
}

func TestRunAndQueryCommands(t *testing.T) {
	withMaxPlayers(t, 2)
	c := New()
	c.state.QueueOpen = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	players := testPlayers(2)
	for _, p := range players {
		resp := make(chan error, 1)
		c.Send(JoinQueue{Player: p, Response: resp})
		if err := <-resp; err != nil {
			t.Fatalf("join failed: %v", err)
		}
	}

	queue, matches, _, queueOpen := c.GetState()
	if len(queue) != 0 {
		t.Fatalf("expected empty queue after acceptance start, got %d", len(queue))
	}
	if len(matches) != 1 {
		t.Fatalf("expected one active match, got %d", len(matches))
	}
	if !queueOpen {
		t.Fatal("expected queue to remain open after match starts")
	}

	match := c.GetPlayerMatch(players[0].SteamID)
	if match == nil {
		t.Fatal("expected player to be assigned to active match")
	}
}

func TestQueueClosedByDefaultAndAdminToggle(t *testing.T) {
	withMaxPlayers(t, 10)
	c := New()
	player := testPlayers(1)[0]

	if c.state.QueueOpen {
		t.Fatal("expected queue to be closed by default")
	}

	err := c.handleJoinQueue(JoinQueue{Player: player})
	if err == nil || err.Error() != "queue is closed" {
		t.Fatalf("expected queue closed error, got %v", err)
	}

	if err := c.handleAdminSetQueueOpen(AdminSetQueueOpen{Open: true}); err != nil {
		t.Fatalf("expected opening queue to succeed, got %v", err)
	}
	if !c.state.QueueOpen {
		t.Fatal("expected queue to be open after admin toggle")
	}

	if err := c.handleJoinQueue(JoinQueue{Player: player}); err != nil {
		t.Fatalf("expected join to work after opening queue, got %v", err)
	}
}

func TestQueuePersistenceCallback(t *testing.T) {
	withMaxPlayers(t, 10)
	c := New()
	c.state.QueueOpen = true
	players := testPlayers(1)

	var saved [][]Player
	c.SetQueuePersistence(func(queue []Player) {
		copyQueue := make([]Player, len(queue))
		copy(copyQueue, queue)
		saved = append(saved, copyQueue)
	})

	if err := c.handleJoinQueue(JoinQueue{Player: players[0]}); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	if err := c.handleLeaveQueue(LeaveQueue{PlayerID: players[0].SteamID}); err != nil {
		t.Fatalf("leave failed: %v", err)
	}

	if len(saved) != 2 {
		t.Fatalf("expected 2 persisted queue snapshots, got %d", len(saved))
	}
	if len(saved[0]) != 1 || saved[0][0].SteamID != players[0].SteamID {
		t.Fatalf("unexpected first persisted snapshot: %+v", saved[0])
	}
	if len(saved[1]) != 0 {
		t.Fatalf("expected empty queue snapshot after leave, got %+v", saved[1])
	}
}
