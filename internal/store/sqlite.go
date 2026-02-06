package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite store and runs migrations.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	store := &SQLiteStore{db: db}

	if err := store.migrate(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS users (
			steam_id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			avatar_url TEXT,
			captain_priority INTEGER DEFAULT 5,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			steam_id TEXT NOT NULL REFERENCES users(steam_id),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at)`,
		`CREATE TABLE IF NOT EXISTS matches (
			id TEXT PRIMARY KEY,
			dota_match_id INTEGER,
			state TEXT NOT NULL,
			started_at TIMESTAMP,
			ended_at TIMESTAMP,
			winner TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS match_players (
			match_id TEXT NOT NULL REFERENCES matches(id),
			steam_id TEXT NOT NULL REFERENCES users(steam_id),
			team TEXT NOT NULL,
			was_captain INTEGER DEFAULT 0,
			accepted INTEGER DEFAULT 0,
			PRIMARY KEY (match_id, steam_id)
		)`,
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	return nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// GetUser retrieves a user by SteamID.
func (s *SQLiteStore) GetUser(ctx context.Context, steamID string) (*User, error) {
	var user User
	err := s.db.QueryRowContext(ctx,
		`SELECT steam_id, name, avatar_url, captain_priority, created_at, updated_at
		 FROM users WHERE steam_id = ?`, steamID).Scan(
		&user.SteamID, &user.Name, &user.AvatarURL,
		&user.CaptainPriority, &user.CreatedAt, &user.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// UpsertUser creates or updates a user.
func (s *SQLiteStore) UpsertUser(ctx context.Context, user *User) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (steam_id, name, avatar_url, captain_priority, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(steam_id) DO UPDATE SET
		 	name = excluded.name,
		 	avatar_url = excluded.avatar_url,
		 	updated_at = excluded.updated_at`,
		user.SteamID, user.Name, user.AvatarURL,
		user.CaptainPriority, user.CreatedAt, user.UpdatedAt,
	)
	return err
}

// CreateSession creates a new session.
func (s *SQLiteStore) CreateSession(ctx context.Context, session *Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, steam_id, created_at, expires_at)
		 VALUES (?, ?, ?, ?)`,
		session.ID, session.SteamID, session.CreatedAt, session.ExpiresAt,
	)
	return err
}

// GetSession retrieves a session by ID.
func (s *SQLiteStore) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	var session Session
	err := s.db.QueryRowContext(ctx,
		`SELECT id, steam_id, created_at, expires_at
		 FROM sessions WHERE id = ? AND expires_at > ?`,
		sessionID, time.Now()).Scan(
		&session.ID, &session.SteamID, &session.CreatedAt, &session.ExpiresAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// DeleteSession removes a session.
func (s *SQLiteStore) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

// DeleteExpiredSessions removes all expired sessions.
func (s *SQLiteStore) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now())
	return err
}

// CreateMatch creates a new match record.
func (s *SQLiteStore) CreateMatch(ctx context.Context, match *Match) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO matches (id, dota_match_id, state, started_at, ended_at, winner)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		match.ID, match.DotaMatchID, match.State, match.StartedAt, match.EndedAt, match.Winner,
	)
	return err
}

// UpdateMatch updates an existing match.
func (s *SQLiteStore) UpdateMatch(ctx context.Context, match *Match) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE matches SET dota_match_id = ?, state = ?, ended_at = ?, winner = ?
		 WHERE id = ?`,
		match.DotaMatchID, match.State, match.EndedAt, match.Winner, match.ID,
	)
	return err
}

// GetMatch retrieves a match by ID.
func (s *SQLiteStore) GetMatch(ctx context.Context, matchID string) (*Match, error) {
	var match Match
	err := s.db.QueryRowContext(ctx,
		`SELECT id, dota_match_id, state, started_at, ended_at, winner
		 FROM matches WHERE id = ?`, matchID).Scan(
		&match.ID, &match.DotaMatchID, &match.State,
		&match.StartedAt, &match.EndedAt, &match.Winner,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &match, nil
}

// AddMatchPlayer adds a player to a match.
func (s *SQLiteStore) AddMatchPlayer(ctx context.Context, mp *MatchPlayer) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO match_players (match_id, steam_id, team, was_captain, accepted)
		 VALUES (?, ?, ?, ?, ?)`,
		mp.MatchID, mp.SteamID, mp.Team, mp.WasCaptain, mp.Accepted,
	)
	return err
}

// GetMatchPlayers retrieves all players for a match.
func (s *SQLiteStore) GetMatchPlayers(ctx context.Context, matchID string) ([]MatchPlayer, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT match_id, steam_id, team, was_captain, accepted
		 FROM match_players WHERE match_id = ?`, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var players []MatchPlayer
	for rows.Next() {
		var mp MatchPlayer
		if err := rows.Scan(&mp.MatchID, &mp.SteamID, &mp.Team, &mp.WasCaptain, &mp.Accepted); err != nil {
			return nil, err
		}
		players = append(players, mp)
	}
	return players, rows.Err()
}
