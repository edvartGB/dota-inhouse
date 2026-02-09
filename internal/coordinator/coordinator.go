package coordinator

import (
	"context"
	"errors"
	"log"
	"math/rand"
	"sort"
	"time"

	"github.com/google/uuid"
)

// MaxPlayers can be overridden via MAX_PLAYERS env var.
var MaxPlayers = 10

const (
	MatchAcceptTimeoutDur = 30 * time.Second
	DraftPickTimeoutDur   = 60 * time.Second
)

// Coordinator owns all mutable state and processes commands sequentially.
type Coordinator struct {
	commands    chan Command
	events      chan Event
	subscribers []chan Event
	state       *State
}

func New() *Coordinator {
	return &Coordinator{
		commands:    make(chan Command, 100),
		events:      make(chan Event, 100),
		subscribers: make([]chan Event, 0),
		state:       NewState(),
	}
}

func (c *Coordinator) Send(cmd Command) {
	c.commands <- cmd
}

func (c *Coordinator) Events() <-chan Event {
	return c.events
}

func (c *Coordinator) Subscribe() <-chan Event {
	ch := make(chan Event, 100)
	c.subscribers = append(c.subscribers, ch)
	return ch
}

func (c *Coordinator) Run(ctx context.Context) {
	log.Println("Coordinator started")
	for {
		select {
		case <-ctx.Done():
			log.Println("Coordinator shutting down")
			return
		case cmd := <-c.commands:
			c.handleCommand(cmd)
		}
	}
}

func (c *Coordinator) emit(e Event) {
	select {
	case c.events <- e:
	default:
		log.Println("Warning: main event channel full, dropping event")
	}

	for _, ch := range c.subscribers {
		select {
		case ch <- e:
		default:
			log.Println("Warning: subscriber event channel full, dropping event")
		}
	}
}

func (c *Coordinator) handleCommand(cmd Command) {
	switch cmd := cmd.(type) {
	case JoinQueue:
		err := c.handleJoinQueue(cmd)
		if cmd.Response != nil {
			cmd.Response <- err
		}
	case LeaveQueue:
		err := c.handleLeaveQueue(cmd)
		if cmd.Response != nil {
			cmd.Response <- err
		}
	case AcceptMatch:
		err := c.handleAcceptMatch(cmd)
		if cmd.Response != nil {
			cmd.Response <- err
		}
	case PickPlayer:
		err := c.handlePickPlayer(cmd)
		if cmd.Response != nil {
			cmd.Response <- err
		}
	case MatchAcceptTimeout:
		c.handleMatchAcceptTimeout(cmd)
	case BotLobbyReady:
		c.handleBotLobbyReady(cmd)
	case BotGameStarted:
		c.handleBotGameStarted(cmd)
	case BotGameEnded:
		c.handleBotGameEnded(cmd)
	case DraftPickTimeout:
		c.handleDraftPickTimeout(cmd)
	case BotLobbyTimeout:
		c.handleBotLobbyTimeout(cmd)
	case AdminCancelMatch:
		cmd.Response <- c.handleAdminCancelMatch(cmd)
	case AdminSetMatchResult:
		cmd.Response <- c.handleAdminSetMatchResult(cmd)
	case AdminKickFromQueue:
		cmd.Response <- c.handleAdminKickFromQueue(cmd)
	case AdminSetLobbySettings:
		cmd.Response <- c.handleAdminSetLobbySettings(cmd)
	case getStateCmd:
		cmd.Response <- stateSnapshot{
			Queue:         c.state.Queue,
			Matches:       c.state.Matches,
			LobbySettings: c.state.LobbySettings,
		}
	case getPlayerMatchCmd:
		cmd.Response <- c.state.GetPlayerMatch(cmd.PlayerID)
	}
}

func (c *Coordinator) handleJoinQueue(cmd JoinQueue) error {
	if c.state.IsPlayerInQueue(cmd.Player.SteamID) {
		return errors.New("already in queue")
	}

	if c.state.IsPlayerInMatch(cmd.Player.SteamID) {
		return errors.New("already in a match")
	}

	c.state.Queue = append(c.state.Queue, cmd.Player)
	log.Printf("Player %s joined queue (%d/%d)", cmd.Player.Name, len(c.state.Queue), MaxPlayers)

	c.emit(QueueUpdated{Queue: c.state.Queue})

	if len(c.state.Queue) >= MaxPlayers {
		c.startMatchAcceptance()
	}

	return nil
}

func (c *Coordinator) handleLeaveQueue(cmd LeaveQueue) error {
	if c.state.IsPlayerInMatch(cmd.PlayerID) {
		return errors.New("cannot leave queue while in a match")
	}

	if !c.state.RemoveFromQueue(cmd.PlayerID) {
		return errors.New("not in queue")
	}

	log.Printf("Player %s left queue (%d/%d)", cmd.PlayerID, len(c.state.Queue), MaxPlayers)
	c.emit(QueueUpdated{Queue: c.state.Queue})

	return nil
}

func (c *Coordinator) startMatchAcceptance() {
	players := make([]Player, MaxPlayers)
	copy(players, c.state.Queue[:MaxPlayers])
	c.state.Queue = c.state.Queue[MaxPlayers:]

	matchID := uuid.New().String()
	deadline := time.Now().Add(MatchAcceptTimeoutDur)

	match := &Match{
		ID:              matchID,
		State:           MatchStateAccepting,
		Players:         players,
		AcceptedPlayers: make(map[string]bool),
		AcceptDeadline:  deadline,
	}
	c.state.Matches[matchID] = match

	log.Printf("Match %s started acceptance phase (%d active matches)", matchID, len(c.state.Matches))

	c.emit(QueueUpdated{Queue: c.state.Queue})
	c.emit(MatchAcceptStarted{
		MatchID:  matchID,
		Players:  players,
		Deadline: deadline,
	})

	go func() {
		time.Sleep(MatchAcceptTimeoutDur)
		c.Send(MatchAcceptTimeout{
			MatchID:   matchID,
			StartedAt: deadline.Add(-MatchAcceptTimeoutDur),
		})
	}()
}

func (c *Coordinator) handleAcceptMatch(cmd AcceptMatch) error {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil {
		return errors.New("match not found")
	}

	if match.State != MatchStateAccepting {
		return errors.New("match not in accepting state")
	}

	found := false
	for _, p := range match.Players {
		if p.SteamID == cmd.PlayerID {
			found = true
			break
		}
	}
	if !found {
		return errors.New("player not in this match")
	}

	match.AcceptedPlayers[cmd.PlayerID] = true
	log.Printf("Player %s accepted match %s (%d/%d)", cmd.PlayerID, cmd.MatchID, len(match.AcceptedPlayers), MaxPlayers)

	c.emit(MatchAcceptUpdated{
		MatchID:  match.ID,
		Accepted: match.AcceptedPlayers,
	})

	if len(match.AcceptedPlayers) >= MaxPlayers {
		c.startDraft(match)
	}

	return nil
}

func (c *Coordinator) handleMatchAcceptTimeout(cmd MatchAcceptTimeout) {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil {
		return // Match already ended
	}

	if match.State != MatchStateAccepting {
		return // Already moved past accepting
	}

	log.Printf("Match %s accept timeout", cmd.MatchID)

	var failedPlayers []Player
	var acceptedPlayers []Player

	for _, p := range match.Players {
		if match.AcceptedPlayers[p.SteamID] {
			acceptedPlayers = append(acceptedPlayers, p)
		} else {
			failedPlayers = append(failedPlayers, p)
			c.emit(PlayerFailedAccept{PlayerID: p.SteamID})
		}
	}

	c.state.Queue = append(acceptedPlayers, c.state.Queue...)

	c.emit(MatchCancelled{
		MatchID:       cmd.MatchID,
		FailedPlayers: failedPlayers,
	})
	c.emit(QueueUpdated{Queue: c.state.Queue})

	delete(c.state.Matches, cmd.MatchID)

	if len(c.state.Queue) >= MaxPlayers {
		c.startMatchAcceptance()
	}
}

func (c *Coordinator) startDraft(match *Match) {
	if match == nil || match.State != MatchStateAccepting {
		return
	}

	captains := selectCaptains(match.Players)

	var available []Player
	for _, p := range match.Players {
		if p.SteamID != captains[0].SteamID && p.SteamID != captains[1].SteamID {
			available = append(available, p)
		}
	}

	match.State = MatchStateDrafting
	match.Captains = captains
	match.Radiant = []Player{captains[0]}
	match.Dire = []Player{captains[1]}
	match.AvailablePlayers = available
	match.CurrentPicker = 0 // Radiant picks first
	match.PickCount = 0

	log.Printf("Match %s started draft phase. Captains: %s (priority %d, Radiant), %s (priority %d, Dire)",
		match.ID, captains[0].Name, captains[0].CaptainPriority, captains[1].Name, captains[1].CaptainPriority)

	c.emit(DraftStarted{
		MatchID:   match.ID,
		Captains:  captains,
		Radiant:   match.Radiant,
		Dire:      match.Dire,
		Available: available,
	})

	// If no players to pick (e.g. 2-player match), complete draft immediately
	if len(available) == 0 {
		c.completeDraft(match)
		return
	}

	c.scheduleDraftTimeout(match.ID, 0)
}

func (c *Coordinator) handlePickPlayer(cmd PickPlayer) error {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil {
		return errors.New("match not found")
	}

	if match.State != MatchStateDrafting {
		return errors.New("match not in drafting state")
	}

	currentCaptain := match.Captains[match.CurrentPicker]
	if currentCaptain.SteamID != cmd.CaptainID {
		return errors.New("not your turn to pick")
	}

	var pickedPlayer *Player
	for i, p := range match.AvailablePlayers {
		if p.SteamID == cmd.PickedID {
			pickedPlayer = &p
			match.AvailablePlayers = append(
				match.AvailablePlayers[:i],
				match.AvailablePlayers[i+1:]...,
			)
			break
		}
	}

	if pickedPlayer == nil {
		return errors.New("player not available for picking")
	}

	if match.CurrentPicker == 0 {
		match.Radiant = append(match.Radiant, *pickedPlayer)
	} else {
		match.Dire = append(match.Dire, *pickedPlayer)
	}

	log.Printf("Captain %s picked %s for %s (pick %d)",
		currentCaptain.Name, pickedPlayer.Name,
		map[int]string{0: "Radiant", 1: "Dire"}[match.CurrentPicker], match.PickCount+1)

	match.PickCount++
	match.CurrentPicker = getPickerForPickCount(match.PickCount)

	c.emit(DraftUpdated{
		MatchID:          match.ID,
		Captains:         match.Captains,
		AvailablePlayers: match.AvailablePlayers,
		Radiant:          match.Radiant,
		Dire:             match.Dire,
		CurrentPicker:    match.CurrentPicker,
	})

	if len(match.AvailablePlayers) == 0 {
		c.completeDraft(match)
	} else {
		c.scheduleDraftTimeout(match.ID, match.PickCount)
	}

	return nil
}

func (c *Coordinator) completeDraft(match *Match) {
	if match == nil {
		return
	}

	match.State = MatchStateWaitingForBot

	log.Printf("Match %s draft complete, requesting bot lobby", match.ID)

	c.emit(RequestBotLobby{
		MatchID:  match.ID,
		Players:  match.Players,
		Radiant:  match.Radiant,
		Dire:     match.Dire,
		GameMode: c.state.LobbySettings.GameMode,
	})
}

func (c *Coordinator) scheduleDraftTimeout(matchID string, pickNumber int) {
	go func() {
		time.Sleep(DraftPickTimeoutDur)
		c.Send(DraftPickTimeout{
			MatchID:    matchID,
			PickNumber: pickNumber,
		})
	}()
}

func (c *Coordinator) handleDraftPickTimeout(cmd DraftPickTimeout) {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil {
		return // Match already ended
	}

	if match.State != MatchStateDrafting {
		return // No longer in drafting phase
	}

	// Stale timeout — pick was already made
	if match.PickCount != cmd.PickNumber {
		return
	}

	failedCaptain := match.Captains[match.CurrentPicker]
	log.Printf("Match %s: Captain %s failed to pick in time", cmd.MatchID, failedCaptain.Name)

	var returnToQueue []Player
	for _, p := range match.Players {
		if p.SteamID != failedCaptain.SteamID {
			returnToQueue = append(returnToQueue, p)
		}
	}

	c.state.Queue = append(returnToQueue, c.state.Queue...)

	c.emit(DraftCancelled{
		MatchID:         cmd.MatchID,
		FailedCaptain:   failedCaptain,
		ReturnedToQueue: returnToQueue,
	})
	c.emit(QueueUpdated{Queue: c.state.Queue})

	delete(c.state.Matches, cmd.MatchID)

	if len(c.state.Queue) >= MaxPlayers {
		c.startMatchAcceptance()
	}
}

func (c *Coordinator) handleBotLobbyReady(cmd BotLobbyReady) {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil {
		return
	}
	log.Printf("Match %s: bot lobby ready", cmd.MatchID)
}

func (c *Coordinator) handleBotLobbyTimeout(cmd BotLobbyTimeout) {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil {
		return // Match already ended
	}

	if match.State != MatchStateWaitingForBot {
		return // Game already started
	}

	log.Printf("Match %s: lobby join timeout", cmd.MatchID)

	joinedCorrectly := make(map[string]bool)
	for _, steamID := range cmd.PlayersJoinedRight {
		joinedCorrectly[steamID] = true
	}

	var returnToQueue []Player
	var failedPlayers []Player
	for _, p := range match.Players {
		if joinedCorrectly[p.SteamID] {
			returnToQueue = append(returnToQueue, p)
		} else {
			failedPlayers = append(failedPlayers, p)
		}
	}

	log.Printf("Match %s: %d players joined correctly, %d failed",
		cmd.MatchID, len(returnToQueue), len(failedPlayers))

	c.state.Queue = append(returnToQueue, c.state.Queue...)

	c.emit(LobbyCancelled{
		MatchID:         cmd.MatchID,
		FailedPlayers:   failedPlayers,
		ReturnedToQueue: returnToQueue,
	})
	c.emit(QueueUpdated{Queue: c.state.Queue})

	delete(c.state.Matches, cmd.MatchID)

	if len(c.state.Queue) >= MaxPlayers {
		c.startMatchAcceptance()
	}
}

func (c *Coordinator) handleBotGameStarted(cmd BotGameStarted) {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil {
		return
	}

	match.State = MatchStateInProgress
	match.DotaMatchID = cmd.DotaMatchID

	log.Printf("Match %s started (Dota Match ID: %d)", cmd.MatchID, cmd.DotaMatchID)

	c.emit(MatchStarted{
		MatchID:     cmd.MatchID,
		DotaMatchID: cmd.DotaMatchID,
		Players:     match.Players,
		Radiant:     match.Radiant,
		Dire:        match.Dire,
		Captains:    match.Captains,
	})
}

func (c *Coordinator) handleBotGameEnded(cmd BotGameEnded) {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil {
		return
	}

	log.Printf("Match %s ended (Dota Match ID: %d)", cmd.MatchID, cmd.DotaMatchID)

	c.emit(MatchCompleted{
		MatchID:     cmd.MatchID,
		DotaMatchID: cmd.DotaMatchID,
		Players:     match.Players,
		Radiant:     match.Radiant,
		Dire:        match.Dire,
		Winner:      cmd.Winner,
	})

	delete(c.state.Matches, cmd.MatchID)

	if len(c.state.Queue) >= MaxPlayers {
		c.startMatchAcceptance()
	}
}

type stateSnapshot struct {
	Queue         []Player
	Matches       map[string]*Match
	LobbySettings LobbySettings
}

func (c *Coordinator) GetState() ([]Player, map[string]*Match, LobbySettings) {
	respCh := make(chan stateSnapshot, 1)
	c.commands <- getStateCmd{Response: respCh}
	resp := <-respCh
	return resp.Queue, resp.Matches, resp.LobbySettings
}

func (c *Coordinator) GetPlayerMatch(playerID string) *Match {
	respCh := make(chan *Match, 1)
	c.commands <- getPlayerMatchCmd{PlayerID: playerID, Response: respCh}
	return <-respCh
}

type getStateCmd struct {
	Response chan stateSnapshot
}

func (getStateCmd) command() {}

type getPlayerMatchCmd struct {
	PlayerID string
	Response chan *Match
}

func (getPlayerMatchCmd) command() {}

// selectCaptains picks two captains weighted by CaptainPriority.
// Equal priorities are broken randomly.
func selectCaptains(players []Player) [2]Player {
	if len(players) < 2 {
		return [2]Player{}
	}

	sorted := make([]Player, len(players))
	copy(sorted, players)

	// Shuffle to randomize equal-priority players
	rand.Shuffle(len(sorted), func(i, j int) {
		sorted[i], sorted[j] = sorted[j], sorted[i]
	})

	// Stable sort keeps random order within same priority
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].CaptainPriority > sorted[j].CaptainPriority
	})

	return [2]Player{sorted[0], sorted[1]}
}

// getPickerForPickCount returns which captain (0=Radiant, 1=Dire) picks
// at the given pick number. Uses 1-2-2-2-1 draft order:
//
//	Pick 0: Radiant, Pick 1-2: Dire, Pick 3-4: Radiant,
//	Pick 5-6: Dire, Pick 7: Radiant
func getPickerForPickCount(pickCount int) int {
	switch pickCount {
	case 0:
		return 0 // Radiant
	case 1, 2:
		return 1 // Dire
	case 3, 4:
		return 0 // Radiant
	case 5, 6:
		return 1 // Dire
	case 7:
		return 0 // Radiant
	default:
		return pickCount % 2
	}
}

func (c *Coordinator) handleAdminCancelMatch(cmd AdminCancelMatch) error {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil {
		return errors.New("match not found")
	}

	log.Printf("Admin cancelled match %s (state: %v, return to queue: %v)", cmd.MatchID, match.State, cmd.ReturnToQueue)

	if cmd.ReturnToQueue {
		for _, p := range match.Players {
			if !c.state.IsPlayerInQueue(p.SteamID) {
				c.state.Queue = append(c.state.Queue, p)
			}
		}
	}

	c.emit(MatchCancelledByAdmin{
		MatchID:         cmd.MatchID,
		ReturnedToQueue: cmd.ReturnToQueue,
		Players:         match.Players,
	})
	c.emit(QueueUpdated{Queue: c.state.Queue})

	delete(c.state.Matches, cmd.MatchID)

	if len(c.state.Queue) >= MaxPlayers {
		c.startMatchAcceptance()
	}

	return nil
}

func (c *Coordinator) handleAdminSetMatchResult(cmd AdminSetMatchResult) error {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil {
		return errors.New("match not found")
	}

	if cmd.Winner != "radiant" && cmd.Winner != "dire" {
		return errors.New("winner must be 'radiant' or 'dire'")
	}

	log.Printf("Admin set match %s result: %s wins", cmd.MatchID, cmd.Winner)

	winner := cmd.Winner
	c.emit(MatchCompleted{
		MatchID:     cmd.MatchID,
		DotaMatchID: match.DotaMatchID,
		Players:     match.Players,
		Radiant:     match.Radiant,
		Dire:        match.Dire,
		Winner:      &winner,
	})

	delete(c.state.Matches, cmd.MatchID)

	if len(c.state.Queue) >= MaxPlayers {
		c.startMatchAcceptance()
	}

	return nil
}

func (c *Coordinator) handleAdminKickFromQueue(cmd AdminKickFromQueue) error {
	found := false
	newQueue := make([]Player, 0, len(c.state.Queue))
	for _, p := range c.state.Queue {
		if p.SteamID == cmd.PlayerID {
			found = true
			log.Printf("Admin kicked player %s from queue", p.Name)
		} else {
			newQueue = append(newQueue, p)
		}
	}

	if !found {
		return errors.New("player not in queue")
	}

	c.state.Queue = newQueue
	c.emit(QueueUpdated{Queue: c.state.Queue})

	return nil
}

func (c *Coordinator) handleAdminSetLobbySettings(cmd AdminSetLobbySettings) error {
	if _, ok := ValidGameModes[cmd.Settings.GameMode]; !ok {
		return errors.New("invalid game mode")
	}

	c.state.LobbySettings = cmd.Settings
	log.Printf("Admin updated lobby settings: game mode = %s", cmd.Settings.GameMode)

	return nil
}
