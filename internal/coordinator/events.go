package coordinator

import "time"

type Event interface {
	event() // marker method
}

type QueueUpdated struct {
	Queue []Player
}

func (QueueUpdated) event() {}

type MatchAcceptStarted struct {
	MatchID  string
	Players  []Player
	Deadline time.Time
}

func (MatchAcceptStarted) event() {}

type MatchAcceptUpdated struct {
	MatchID  string
	Accepted map[string]bool
}

func (MatchAcceptUpdated) event() {}

type DraftStarted struct {
	MatchID   string
	Captains  [2]Player
	Radiant   []Player
	Dire      []Player
	Available []Player
}

func (DraftStarted) event() {}

type DraftUpdated struct {
	MatchID          string
	Captains         [2]Player
	AvailablePlayers []Player
	Radiant          []Player
	Dire             []Player
	CurrentPicker    int
}

func (DraftUpdated) event() {}

type PlayerFailedAccept struct {
	PlayerID string
}

func (PlayerFailedAccept) event() {}

type MatchCancelled struct {
	MatchID       string
	FailedPlayers []string
}

func (MatchCancelled) event() {}

type RequestBotLobby struct {
	MatchID  string
	Players  []Player
	Radiant  []Player
	Dire     []Player
	GameMode string // "cm", "ap", "cd", "rd", "ar"
}

func (RequestBotLobby) event() {}

type MatchStarted struct {
	MatchID     string
	DotaMatchID uint64
}

func (MatchStarted) event() {}

type MatchCompleted struct {
	MatchID     string
	DotaMatchID uint64
	Players     []Player
	Radiant     []Player
	Dire        []Player
	Winner      *string // "radiant", "dire", or nil if unknown
}

func (MatchCompleted) event() {}

type DraftCancelled struct {
	MatchID         string
	FailedCaptain   Player
	ReturnedToQueue []Player
}

func (DraftCancelled) event() {}

type LobbyCancelled struct {
	MatchID         string
	FailedPlayers   []Player // Players who didn't join or joined wrong team
	ReturnedToQueue []Player // Players who joined correctly, returned to queue
}

func (LobbyCancelled) event() {}

type MatchCancelledByAdmin struct {
	MatchID         string
	ReturnedToQueue bool
	Players         []Player
}

func (MatchCancelledByAdmin) event() {}
