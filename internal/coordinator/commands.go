package coordinator

import "time"

// Command is the interface for all commands sent to the coordinator.
type Command interface {
	command() // marker method
}

// JoinQueue requests to add a player to the queue.
type JoinQueue struct {
	Player   Player
	Response chan error
}

func (JoinQueue) command() {}

// LeaveQueue requests to remove a player from the queue.
type LeaveQueue struct {
	PlayerID string
	Response chan error
}

func (LeaveQueue) command() {}

// AcceptMatch signals that a player has accepted the match.
type AcceptMatch struct {
	PlayerID string
	MatchID  string
	Response chan error
}

func (AcceptMatch) command() {}

// PickPlayer is sent by a captain to draft a player.
type PickPlayer struct {
	CaptainID string
	PickedID  string
	MatchID   string
	Response  chan error
}

func (PickPlayer) command() {}

// MatchAcceptTimeout is sent when the accept phase times out.
type MatchAcceptTimeout struct {
	MatchID   string
	StartedAt time.Time
}

func (MatchAcceptTimeout) command() {}

// BotLobbyReady signals that the Dota 2 lobby has been created.
type BotLobbyReady struct {
	MatchID string
}

func (BotLobbyReady) command() {}

// BotGameStarted signals that the Dota 2 game has started.
type BotGameStarted struct {
	MatchID     string
	DotaMatchID uint64
}

func (BotGameStarted) command() {}

// BotGameEnded signals that the Dota 2 game has ended.
type BotGameEnded struct {
	MatchID     string
	DotaMatchID uint64
	Winner      *string // "radiant", "dire", or nil if unknown
}

func (BotGameEnded) command() {}

// DraftPickTimeout is sent when a captain fails to pick in time.
type DraftPickTimeout struct {
	MatchID    string
	PickNumber int // Which pick this timeout was for
}

func (DraftPickTimeout) command() {}

// BotLobbyTimeout is sent when players fail to join the lobby in time.
type BotLobbyTimeout struct {
	MatchID            string
	PlayersJoinedRight []string // Steam IDs of players who joined on correct team
}

func (BotLobbyTimeout) command() {}

// AdminCancelMatch cancels any match regardless of state.
type AdminCancelMatch struct {
	MatchID        string
	ReturnToQueue  bool // If true, return players to queue
	Response       chan error
}

func (AdminCancelMatch) command() {}

// AdminSetMatchResult manually sets the result of a match.
type AdminSetMatchResult struct {
	MatchID  string
	Winner   string // "radiant" or "dire"
	Response chan error
}

func (AdminSetMatchResult) command() {}

// AdminKickFromQueue removes a player from the queue.
type AdminKickFromQueue struct {
	PlayerID string
	Response chan error
}

func (AdminKickFromQueue) command() {}

// AdminSetLobbySettings updates the lobby settings.
type AdminSetLobbySettings struct {
	Settings LobbySettings
	Response chan error
}

func (AdminSetLobbySettings) command() {}
