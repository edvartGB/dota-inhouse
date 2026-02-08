package coordinator

import "time"

type Command interface {
	command() // marker method
}

type JoinQueue struct {
	Player   Player
	Response chan error
}

func (JoinQueue) command() {}

type LeaveQueue struct {
	PlayerID string
	Response chan error
}

func (LeaveQueue) command() {}

type AcceptMatch struct {
	PlayerID string
	MatchID  string
	Response chan error
}

func (AcceptMatch) command() {}

type PickPlayer struct {
	CaptainID string
	PickedID  string
	MatchID   string
	Response  chan error
}

func (PickPlayer) command() {}

type MatchAcceptTimeout struct {
	MatchID   string
	StartedAt time.Time
}

func (MatchAcceptTimeout) command() {}

type BotLobbyReady struct {
	MatchID string
}

func (BotLobbyReady) command() {}

type BotGameStarted struct {
	MatchID     string
	DotaMatchID uint64
}

func (BotGameStarted) command() {}

type BotGameEnded struct {
	MatchID     string
	DotaMatchID uint64
	Winner      *string // "radiant", "dire", or nil if unknown
}

func (BotGameEnded) command() {}

type DraftPickTimeout struct {
	MatchID    string
	PickNumber int // Which pick this timeout was for
}

func (DraftPickTimeout) command() {}

type BotLobbyTimeout struct {
	MatchID            string
	PlayersJoinedRight []string // Steam IDs of players who joined on correct team
}

func (BotLobbyTimeout) command() {}

type AdminCancelMatch struct {
	MatchID       string
	ReturnToQueue bool // If true, return players to queue
	Response      chan error
}

func (AdminCancelMatch) command() {}

type AdminSetMatchResult struct {
	MatchID  string
	Winner   string // "radiant" or "dire"
	Response chan error
}

func (AdminSetMatchResult) command() {}

type AdminKickFromQueue struct {
	PlayerID string
	Response chan error
}

func (AdminKickFromQueue) command() {}

type AdminSetLobbySettings struct {
	Settings LobbySettings
	Response chan error
}

func (AdminSetLobbySettings) command() {}
