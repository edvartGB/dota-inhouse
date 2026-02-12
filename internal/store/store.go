package store

import (
	"context"
	"time"
)

type User struct {
	SteamID         string
	Name            string
	AvatarURL       string
	CaptainPriority int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Session struct {
	ID        string
	SteamID   string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type Match struct {
	ID          string
	DotaMatchID uint64
	State       string
	StartedAt   time.Time
	EndedAt     *time.Time
	Winner      *string
	Duration    *int // Duration in seconds
}

type MatchPlayer struct {
	MatchID    string
	SteamID    string
	Team       string
	WasCaptain bool
	Accepted   bool
}

type MatchPlayerInfo struct {
	SteamID    string
	Name       string
	AvatarURL  string
	Team       string
	WasCaptain bool
}

type Store interface {
	GetUser(ctx context.Context, steamID string) (*User, error)
	UpsertUser(ctx context.Context, user *User) error
	ListUsers(ctx context.Context) ([]User, error)
	UpdateCaptainPriority(ctx context.Context, steamID string, priority int) error

	CreateSession(ctx context.Context, session *Session) error
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	DeleteExpiredSessions(ctx context.Context) error

	CreateMatch(ctx context.Context, match *Match) error
	UpdateMatch(ctx context.Context, match *Match) error
	GetMatch(ctx context.Context, matchID string) (*Match, error)
	SetMatchWinner(ctx context.Context, matchID string, winner string) error

	AddMatchPlayer(ctx context.Context, mp *MatchPlayer) error
	GetMatchPlayers(ctx context.Context, matchID string) ([]MatchPlayer, error)

	ListMatches(ctx context.Context, limit int) ([]Match, error)
	ListMatchesWithPlayers(ctx context.Context, limit int) ([]MatchWithPlayers, error)

	GetLeaderboard(ctx context.Context, startDate, endDate *time.Time) ([]LeaderboardEntry, error)

	// Push subscriptions
	SavePushSubscription(ctx context.Context, sub *PushSubscription) error
	GetPushSubscriptions(ctx context.Context, steamID string) ([]PushSubscription, error)
	GetAllPushSubscriptions(ctx context.Context) ([]PushSubscription, error)
	DeletePushSubscription(ctx context.Context, endpoint string) error

	Close() error
}

type MatchWithPlayers struct {
	Match
	Radiant        []MatchPlayerInfo
	Dire           []MatchPlayerInfo
	RadiantCaptain *MatchPlayerInfo
	DireCaptain    *MatchPlayerInfo
}

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

type PushSubscription struct {
	ID        int
	SteamID   string
	Endpoint  string
	P256dh    string
	Auth      string
	CreatedAt time.Time
}
