package coordinator

import "time"

// Event is the interface for all events emitted by the coordinator.
type Event interface {
	event() // marker method
}

// QueueUpdated is emitted when the queue changes.
type QueueUpdated struct {
	Queue []Player
}

func (QueueUpdated) event() {}

// MatchAcceptStarted is emitted when a match begins the accept phase.
type MatchAcceptStarted struct {
	MatchID  string
	Players  []Player
	Deadline time.Time
}

func (MatchAcceptStarted) event() {}

// MatchAcceptUpdated is emitted when a player accepts.
type MatchAcceptUpdated struct {
	MatchID  string
	Accepted map[string]bool
}

func (MatchAcceptUpdated) event() {}

// DraftStarted is emitted when the draft phase begins.
type DraftStarted struct {
	MatchID  string
	Captains [2]Player
	Radiant  []Player
	Dire     []Player
	Available []Player
}

func (DraftStarted) event() {}

// DraftUpdated is emitted when a player is picked.
type DraftUpdated struct {
	MatchID          string
	Captains         [2]Player
	AvailablePlayers []Player
	Radiant          []Player
	Dire             []Player
	CurrentPicker    int
}

func (DraftUpdated) event() {}

// PlayerFailedAccept is emitted to notify a specific player they failed to accept.
type PlayerFailedAccept struct {
	PlayerID string
}

func (PlayerFailedAccept) event() {}

// MatchCancelled is emitted when a match is cancelled due to failed accepts.
type MatchCancelled struct {
	MatchID       string
	FailedPlayers []string
}

func (MatchCancelled) event() {}

// RequestBotLobby is emitted when the draft is complete and a bot should create the lobby.
type RequestBotLobby struct {
	MatchID string
	Players []Player
	Radiant []Player
	Dire    []Player
}

func (RequestBotLobby) event() {}

// MatchStarted is emitted when the Dota 2 game starts.
type MatchStarted struct {
	MatchID     string
	DotaMatchID uint64
}

func (MatchStarted) event() {}

// MatchCompleted is emitted when the Dota 2 game ends.
type MatchCompleted struct {
	MatchID     string
	DotaMatchID uint64
	Players     []Player
}

func (MatchCompleted) event() {}

// DraftCancelled is emitted when a captain fails to pick in time.
type DraftCancelled struct {
	MatchID         string
	FailedCaptain   Player
	ReturnedToQueue []Player
}

func (DraftCancelled) event() {}

// LobbyCancelled is emitted when players fail to join the lobby in time.
type LobbyCancelled struct {
	MatchID         string
	FailedPlayers   []Player // Players who didn't join or joined wrong team
	ReturnedToQueue []Player // Players who joined correctly, returned to queue
}

func (LobbyCancelled) event() {}
