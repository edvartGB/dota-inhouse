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
	MatchAcceptTimeoutDur = 60 * time.Second
	SideChoiceTimeoutDur  = 60 * time.Second
	DraftPickTimeoutDur   = 60 * time.Second
	LobbyJoinTimeoutDur   = 5 * time.Minute
)

// Coordinator owns all mutable state and processes commands sequentially.
type Coordinator struct {
	commands     chan Command
	events       chan Event
	subscribers  []chan Event
	state        *State
	persistQueue func([]Player)
}

func New() *Coordinator {
	return &Coordinator{
		commands:    make(chan Command, 100),
		events:      make(chan Event, 100),
		subscribers: make([]chan Event, 0),
		state:       NewState(),
	}
}

// SetQueuePersistence sets a callback that is called whenever the queue changes.
func (c *Coordinator) SetQueuePersistence(fn func([]Player)) {
	c.persistQueue = fn
}

// RestoreQueue sets the initial queue state. Must be called before Run.
func (c *Coordinator) RestoreQueue(players []Player) {
	c.state.Queue = players
}

// SetInitialLobbySettings sets lobby settings before Run.
func (c *Coordinator) SetInitialLobbySettings(settings LobbySettings) error {
	if _, ok := ValidGameModes[settings.GameMode]; !ok {
		return errors.New("invalid game mode")
	}
	c.state.LobbySettings = settings
	return nil
}

func (c *Coordinator) saveQueue() {
	if c.persistQueue != nil {
		c.persistQueue(c.state.Queue)
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
			// Return players from non-in-progress matches to queue before saving
			for _, match := range c.state.Matches {
				if match.State != MatchStateInProgress {
					for _, p := range match.Players {
						if !c.state.IsPlayerInQueue(p.SteamID) {
							c.state.Queue = append(c.state.Queue, p)
						}
					}
				}
			}
			c.saveQueue()
			return
		case cmd := <-c.commands:
			c.handleCommand(cmd)
		}
	}
}

func (c *Coordinator) emit(e Event) {
	if _, ok := e.(QueueUpdated); ok {
		c.saveQueue()
	}

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
	case ChooseSide:
		err := c.handleChooseSide(cmd)
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
	case BotLiveHeroesUpdated:
		c.handleBotLiveHeroesUpdated(cmd)
	case SideChoiceTimeout:
		c.handleSideChoiceTimeout(cmd)
	case DraftPickTimeout:
		c.handleDraftPickTimeout(cmd)
	case BotLobbyTimeout:
		c.handleBotLobbyTimeout(cmd)
	case AdminPauseLobbyCountdown:
		cmd.Response <- c.handleAdminPauseLobbyCountdown(cmd)
	case AdminResumeLobbyCountdown:
		cmd.Response <- c.handleAdminResumeLobbyCountdown(cmd)
	case AdminCancelMatch:
		cmd.Response <- c.handleAdminCancelMatch(cmd)
	case AdminSetMatchResult:
		cmd.Response <- c.handleAdminSetMatchResult(cmd)
	case AdminKickFromQueue:
		cmd.Response <- c.handleAdminKickFromQueue(cmd)
	case AdminSetLobbySettings:
		cmd.Response <- c.handleAdminSetLobbySettings(cmd)
	case AdminSetQueueOpen:
		cmd.Response <- c.handleAdminSetQueueOpen(cmd)
	case getStateCmd:
		cmd.Response <- stateSnapshot{
			Queue:         c.state.Queue,
			Matches:       c.state.Matches,
			LobbySettings: c.state.LobbySettings,
			QueueOpen:     c.state.QueueOpen,
		}
	case getPlayerMatchCmd:
		cmd.Response <- c.state.GetPlayerMatch(cmd.PlayerID)
	}
}

func (c *Coordinator) handleJoinQueue(cmd JoinQueue) error {
	if !c.state.QueueOpen {
		return errors.New("queue is closed")
	}

	if c.state.IsPlayerInQueue(cmd.Player.SteamID) {
		return errors.New("already in queue")
	}

	if c.state.IsPlayerInMatch(cmd.Player.SteamID) {
		return errors.New("already in a match")
	}

	c.state.Queue = append(c.state.Queue, cmd.Player)
	log.Printf("Player %s joined queue (%d/%d)", cmd.Player.Name, len(c.state.Queue), MaxPlayers)

	c.emit(QueueUpdated{
		Queue:           c.state.Queue,
		ActionPlayerIDs: []string{cmd.Player.SteamID},
	})

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
	c.emit(QueueUpdated{
		Queue:           c.state.Queue,
		ActionPlayerIDs: []string{cmd.PlayerID},
	})

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

	c.emit(QueueUpdated{
		Queue:           c.state.Queue,
		ActionPlayerIDs: playerSteamIDs(players),
	})
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
		PlayerID: cmd.PlayerID,
		Accepted: match.AcceptedPlayers,
	})

	if len(match.AcceptedPlayers) >= MaxPlayers {
		c.startSideChoice(match)
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
	c.emit(QueueUpdated{
		Queue:           c.state.Queue,
		ActionPlayerIDs: playerSteamIDs(match.Players),
	})

	delete(c.state.Matches, cmd.MatchID)

	if len(c.state.Queue) >= MaxPlayers {
		c.startMatchAcceptance()
	}
}

func (c *Coordinator) startSideChoice(match *Match) {
	if match == nil || match.State != MatchStateAccepting {
		return
	}

	captains := selectCaptains(match.Players)
	if captains[0].SteamID == "" || captains[1].SteamID == "" {
		return
	}

	match.State = MatchStateChoosingSide
	match.Captains = captains
	match.SideChooserIndex = getSideChooserIndex(captains)
	match.SideChoiceDeadline = time.Now().Add(SideChoiceTimeoutDur)
	match.AvailablePlayers = nonCaptainPlayers(match.Players, captains)

	chooser := captains[match.SideChooserIndex]
	log.Printf("Match %s started side choice. Chooser: %s (priority %d)", match.ID, chooser.Name, chooser.CaptainPriority)

	c.emit(SideChoiceStarted{
		MatchID:          match.ID,
		Players:          match.Players,
		Captains:         captains,
		SideChooserIndex: match.SideChooserIndex,
		Available:        match.AvailablePlayers,
		Deadline:         match.SideChoiceDeadline,
	})

	c.scheduleSideChoiceTimeout(match.ID)
}

func (c *Coordinator) startDraftWithCaptains(match *Match, captains [2]Player) {
	if match == nil {
		return
	}

	available := nonCaptainPlayers(match.Players, captains)

	match.State = MatchStateDrafting
	match.Captains = captains
	match.Radiant = []Player{captains[0]}
	match.Dire = []Player{captains[1]}
	match.AvailablePlayers = available
	match.CurrentPicker = 0 // Radiant picks first
	match.PickCount = 0
	match.PickDeadline = time.Now().Add(DraftPickTimeoutDur)

	log.Printf("Match %s started draft phase. Captains: %s (priority %d, Radiant), %s (priority %d, Dire)",
		match.ID, captains[0].Name, captains[0].CaptainPriority, captains[1].Name, captains[1].CaptainPriority)

	c.emit(DraftStarted{
		MatchID:   match.ID,
		Captains:  captains,
		Radiant:   match.Radiant,
		Dire:      match.Dire,
		Available: available,
		Deadline:  match.PickDeadline,
	})

	// If no players to pick (e.g. 2-player match), complete draft immediately
	if len(available) == 0 {
		c.completeDraft(match)
		return
	}

	c.scheduleDraftTimeout(match.ID, 0)
}

func (c *Coordinator) handleChooseSide(cmd ChooseSide) error {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil {
		return errors.New("match not found")
	}
	if match.State != MatchStateChoosingSide {
		return errors.New("match not waiting for side choice")
	}
	if cmd.Side != "radiant" && cmd.Side != "dire" {
		return errors.New("side must be 'radiant' or 'dire'")
	}

	chooser := match.Captains[match.SideChooserIndex]
	if chooser.SteamID != cmd.CaptainID {
		return errors.New("not your side choice")
	}

	captains := arrangeCaptainsForSideChoice(match.Captains, match.SideChooserIndex, cmd.Side)
	log.Printf("Match %s side choice: %s chose %s", cmd.MatchID, chooser.Name, cmd.Side)
	c.startDraftWithCaptains(match, captains)
	return nil
}

func (c *Coordinator) scheduleSideChoiceTimeout(matchID string) {
	go func() {
		time.Sleep(SideChoiceTimeoutDur)
		c.Send(SideChoiceTimeout{MatchID: matchID})
	}()
}

func (c *Coordinator) handleSideChoiceTimeout(cmd SideChoiceTimeout) {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil || match.State != MatchStateChoosingSide {
		return
	}

	log.Printf("Match %s side choice timed out; assigning sides randomly", cmd.MatchID)
	c.startDraftWithCaptains(match, assignCaptainSides(match.Captains))
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
	match.PickDeadline = time.Now().Add(DraftPickTimeoutDur)

	c.emit(DraftUpdated{
		MatchID:          match.ID,
		Captains:         match.Captains,
		AvailablePlayers: match.AvailablePlayers,
		Radiant:          match.Radiant,
		Dire:             match.Dire,
		CurrentPicker:    match.CurrentPicker,
		Deadline:         match.PickDeadline,
	})

	// Auto-assign last remaining player
	if len(match.AvailablePlayers) == 1 {
		last := match.AvailablePlayers[0]
		match.AvailablePlayers = nil
		if match.CurrentPicker == 0 {
			match.Radiant = append(match.Radiant, last)
		} else {
			match.Dire = append(match.Dire, last)
		}
		log.Printf("Auto-assigned last player %s to %s",
			last.Name, map[int]string{0: "Radiant", 1: "Dire"}[match.CurrentPicker])
		match.PickCount++
		match.CurrentPicker = getPickerForPickCount(match.PickCount)

		c.emit(DraftUpdated{
			MatchID:          match.ID,
			Captains:         match.Captains,
			AvailablePlayers: match.AvailablePlayers,
			Radiant:          match.Radiant,
			Dire:             match.Dire,
			CurrentPicker:    match.CurrentPicker,
			Deadline:         match.PickDeadline,
		})
	}

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
	match.LobbyDeadline = time.Now().Add(LobbyJoinTimeoutDur)
	match.LobbyPaused = false
	match.LobbyRemaining = 0

	log.Printf("Match %s draft complete, requesting bot lobby", match.ID)

	c.emit(RequestBotLobby{
		MatchID:  match.ID,
		Players:  match.Players,
		Radiant:  match.Radiant,
		Dire:     match.Dire,
		GameMode: c.state.LobbySettings.GameMode,
		LeagueID: c.state.LobbySettings.LeagueID,
		Deadline: match.LobbyDeadline,
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
	c.emit(QueueUpdated{
		Queue:           c.state.Queue,
		ActionPlayerIDs: playerSteamIDs(match.Players),
	})

	delete(c.state.Matches, cmd.MatchID)

	if len(c.state.Queue) >= MaxPlayers {
		c.startMatchAcceptance()
	}
}

func (c *Coordinator) handleAdminPauseLobbyCountdown(cmd AdminPauseLobbyCountdown) error {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil {
		return errors.New("match not found")
	}
	if match.State != MatchStateWaitingForBot {
		return errors.New("match is not waiting for lobby")
	}
	if match.LobbyPaused {
		return errors.New("lobby countdown is already paused")
	}

	remaining := time.Until(match.LobbyDeadline)
	if remaining < 0 {
		remaining = 0
	}
	match.LobbyPaused = true
	match.LobbyRemaining = remaining

	log.Printf("Admin paused lobby countdown for match %s with %v remaining", cmd.MatchID, remaining)
	c.emit(LobbyCountdownPaused{
		MatchID:   cmd.MatchID,
		Players:   match.Players,
		Remaining: remaining,
	})
	return nil
}

func (c *Coordinator) handleAdminResumeLobbyCountdown(cmd AdminResumeLobbyCountdown) error {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil {
		return errors.New("match not found")
	}
	if match.State != MatchStateWaitingForBot {
		return errors.New("match is not waiting for lobby")
	}
	if !match.LobbyPaused {
		return errors.New("lobby countdown is not paused")
	}

	remaining := match.LobbyRemaining
	if remaining <= 0 {
		remaining = time.Second
	}
	match.LobbyDeadline = time.Now().Add(remaining)
	match.LobbyPaused = false
	match.LobbyRemaining = 0

	log.Printf("Admin resumed lobby countdown for match %s; new deadline %s", cmd.MatchID, match.LobbyDeadline.Format(time.RFC3339))
	c.emit(LobbyCountdownResumed{
		MatchID:  cmd.MatchID,
		Players:  match.Players,
		Deadline: match.LobbyDeadline,
	})
	return nil
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

	if !cmd.Deadline.IsZero() {
		if match.LobbyPaused {
			log.Printf("Ignoring lobby join timeout for paused match %s", cmd.MatchID)
			return
		}
		if !match.LobbyDeadline.Equal(cmd.Deadline) {
			log.Printf("Ignoring stale lobby join timeout for match %s", cmd.MatchID)
			return
		}
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
	c.emit(QueueUpdated{
		Queue:           c.state.Queue,
		ActionPlayerIDs: playerSteamIDs(match.Players),
	})

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

	startedAt := time.Now()
	match.State = MatchStateInProgress
	match.DotaMatchID = cmd.DotaMatchID
	match.GameStartedAt = &startedAt

	log.Printf("Match %s started (Dota Match ID: %d)", cmd.MatchID, cmd.DotaMatchID)

	c.emit(MatchStarted{
		MatchID:     cmd.MatchID,
		DotaMatchID: cmd.DotaMatchID,
		StartedAt:   startedAt,
		Players:     match.Players,
		Radiant:     match.Radiant,
		Dire:        match.Dire,
		Captains:    match.Captains,
	})
}

func (c *Coordinator) handleBotLiveHeroesUpdated(cmd BotLiveHeroesUpdated) {
	match := c.state.GetMatch(cmd.MatchID)
	if match == nil {
		return
	}

	// A late sample can arrive after the match has been torn down or moved on;
	// applying it would resurrect hero art on a finished match.
	if match.State != MatchStateInProgress {
		return
	}

	match.Heroes = cmd.Heroes
	match.HeroLevels = cmd.Levels
	match.LiveGameState = cmd.GameState
	match.LiveGameTime = cmd.GameTime

	log.Printf("Match %s live heroes updated (%d players, game_state=%d game_time=%d)",
		cmd.MatchID, len(cmd.Heroes), cmd.GameState, cmd.GameTime)

	c.emit(LiveHeroesUpdated{MatchID: cmd.MatchID})
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
	QueueOpen     bool
}

func (c *Coordinator) GetState() ([]Player, map[string]*Match, LobbySettings, bool) {
	respCh := make(chan stateSnapshot, 1)
	c.commands <- getStateCmd{Response: respCh}
	resp := <-respCh
	return resp.Queue, resp.Matches, resp.LobbySettings, resp.QueueOpen
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

// selectCaptains picks the two highest-priority captains.
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

// nonCaptainPlayers returns the match players who are not one of the two captains.
func nonCaptainPlayers(players []Player, captains [2]Player) []Player {
	var available []Player
	for _, p := range players {
		if p.SteamID != captains[0].SteamID && p.SteamID != captains[1].SteamID {
			available = append(available, p)
		}
	}
	return available
}

// getSideChooserIndex returns the lower-priority selected captain.
// Equal priorities are broken randomly.
func getSideChooserIndex(captains [2]Player) int {
	if captains[0].SteamID == "" || captains[1].SteamID == "" {
		return 0
	}
	if captains[0].CaptainPriority == captains[1].CaptainPriority {
		return rand.Intn(2)
	}
	if captains[0].CaptainPriority < captains[1].CaptainPriority {
		return 0
	}
	return 1
}

func arrangeCaptainsForSideChoice(captains [2]Player, chooserIndex int, side string) [2]Player {
	if chooserIndex != 0 && chooserIndex != 1 {
		chooserIndex = 0
	}
	otherIndex := 1 - chooserIndex

	if side == "radiant" {
		return [2]Player{captains[chooserIndex], captains[otherIndex]}
	}
	return [2]Player{captains[otherIndex], captains[chooserIndex]}
}

// assignCaptainSides orders captains for drafting:
// captains[0] becomes Radiant/first-pick, captains[1] becomes Dire/second-pick.
// Side placement is random and does not affect captain selection priority.
func assignCaptainSides(captains [2]Player) [2]Player {
	if captains[0].SteamID == "" || captains[1].SteamID == "" {
		return captains
	}

	if rand.Intn(2) == 0 {
		return captains
	}

	return [2]Player{captains[1], captains[0]}
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
		// Return cancelled-match players to the front of the queue, preserving
		// their match order and avoiding duplicates.
		matchPlayerIDs := make(map[string]struct{}, len(match.Players))
		front := make([]Player, 0, len(match.Players))
		for _, p := range match.Players {
			matchPlayerIDs[p.SteamID] = struct{}{}
			if !c.state.IsPlayerInQueue(p.SteamID) {
				front = append(front, p)
			}
		}

		back := make([]Player, 0, len(c.state.Queue))
		for _, queued := range c.state.Queue {
			if _, inCancelledMatch := matchPlayerIDs[queued.SteamID]; !inCancelledMatch {
				back = append(back, queued)
			}
		}

		c.state.Queue = append(front, back...)
	}

	c.emit(MatchCancelledByAdmin{
		MatchID:         cmd.MatchID,
		ReturnedToQueue: cmd.ReturnToQueue,
		Players:         match.Players,
	})
	c.emit(QueueUpdated{
		Queue:           c.state.Queue,
		ActionPlayerIDs: playerSteamIDs(match.Players),
	})

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
	c.emit(QueueUpdated{
		Queue:           c.state.Queue,
		ActionPlayerIDs: []string{cmd.PlayerID},
	})

	return nil
}

func (c *Coordinator) handleAdminSetLobbySettings(cmd AdminSetLobbySettings) error {
	if _, ok := ValidGameModes[cmd.Settings.GameMode]; !ok {
		return errors.New("invalid game mode")
	}

	c.state.LobbySettings = cmd.Settings
	log.Printf("Admin updated lobby settings: game mode = %s, league_id = %d", cmd.Settings.GameMode, cmd.Settings.LeagueID)
	c.emit(LobbySettingsUpdated{Settings: cmd.Settings})

	return nil
}

func (c *Coordinator) handleAdminSetQueueOpen(cmd AdminSetQueueOpen) error {
	if c.state.QueueOpen == cmd.Open {
		return nil
	}

	c.state.QueueOpen = cmd.Open
	log.Printf("Admin set queue open=%v", c.state.QueueOpen)
	c.emit(QueueUpdated{
		Queue:         c.state.Queue,
		ActionsForAll: true,
	})
	return nil
}

func playerSteamIDs(players []Player) []string {
	ids := make([]string, 0, len(players))
	for _, p := range players {
		ids = append(ids, p.SteamID)
	}
	return ids
}
