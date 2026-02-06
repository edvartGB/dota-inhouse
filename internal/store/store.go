package store

import (
	"context"
	"time"
)

// User represents a registered user.
type User struct {
	SteamID         string
	Name            string
	AvatarURL       string
	CaptainPriority int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Session represents an active user session.
type Session struct {
	ID        string
	SteamID   string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Match represents a completed match record.
type Match struct {
	ID          string
	DotaMatchID uint64
	State       string
	StartedAt   time.Time
	EndedAt     *time.Time
	Winner      *string
	Duration    *int // Duration in seconds
}

// MatchPlayer represents a player's participation in a match.
type MatchPlayer struct {
	MatchID    string
	SteamID    string
	Team       string
	WasCaptain bool
	Accepted   bool
}

// MatchPlayerInfo includes player name for display.
type MatchPlayerInfo struct {
	SteamID    string
	Name       string
	AvatarURL  string
	Team       string
	WasCaptain bool
}

// Store defines the interface for data persistence.
type Store interface {
	// User operations
	GetUser(ctx context.Context, steamID string) (*User, error)
	UpsertUser(ctx context.Context, user *User) error
	ListUsers(ctx context.Context) ([]User, error)
	UpdateCaptainPriority(ctx context.Context, steamID string, priority int) error

	// Session operations
	CreateSession(ctx context.Context, session *Session) error
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	DeleteExpiredSessions(ctx context.Context) error

	// Match operations
	CreateMatch(ctx context.Context, match *Match) error
	UpdateMatch(ctx context.Context, match *Match) error
	GetMatch(ctx context.Context, matchID string) (*Match, error)

	// Match player operations
	AddMatchPlayer(ctx context.Context, mp *MatchPlayer) error
	GetMatchPlayers(ctx context.Context, matchID string) ([]MatchPlayer, error)

	// Match history
	ListMatches(ctx context.Context, limit int) ([]Match, error)
	ListMatchesWithPlayers(ctx context.Context, limit int) ([]MatchWithPlayers, error)

	// Leaderboard
	GetLeaderboard(ctx context.Context, startDate, endDate *time.Time) ([]LeaderboardEntry, error)

	// Close the store
	Close() error
}

// MatchWithPlayers combines a match with its player data for display.
type MatchWithPlayers struct {
	Match
	Radiant        []MatchPlayerInfo
	Dire           []MatchPlayerInfo
	RadiantCaptain *MatchPlayerInfo
	DireCaptain    *MatchPlayerInfo
}

// LeaderboardEntry represents a player's stats for the leaderboard.
type LeaderboardEntry struct {
	SteamID   string
	Name      string
	AvatarURL string
	Wins      int
	Losses    int
	Total     int
	WinRate   float64
	Streak    int // Positive = win streak, negative = loss streak
}
