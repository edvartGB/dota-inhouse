package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

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
			winner TEXT,
			duration INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS match_players (
			match_id TEXT NOT NULL REFERENCES matches(id),
			steam_id TEXT NOT NULL REFERENCES users(steam_id),
			team TEXT NOT NULL,
			was_captain INTEGER DEFAULT 0,
			accepted INTEGER DEFAULT 0,
			PRIMARY KEY (match_id, steam_id)
		)`,
		`CREATE TABLE IF NOT EXISTS push_subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			steam_id TEXT NOT NULL REFERENCES users(steam_id),
			endpoint TEXT NOT NULL UNIQUE,
			p256dh TEXT NOT NULL,
			auth TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_push_subs_steam_id ON push_subscriptions(steam_id)`,
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	// Run optional migrations that may fail (e.g., adding columns that might already exist)
	optionalMigrations := []string{
		`ALTER TABLE matches ADD COLUMN duration INTEGER`,
	}
	for _, m := range optionalMigrations {
		s.db.Exec(m) // Ignore errors - column may already exist
	}

	return nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

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

func (s *SQLiteStore) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT steam_id, name, avatar_url, captain_priority, created_at, updated_at
		 FROM users ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.SteamID, &u.Name, &u.AvatarURL, &u.CaptainPriority, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *SQLiteStore) UpdateCaptainPriority(ctx context.Context, steamID string, priority int) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE users SET captain_priority = ?, updated_at = ? WHERE steam_id = ?`,
		priority, time.Now(), steamID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func (s *SQLiteStore) CreateSession(ctx context.Context, session *Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, steam_id, created_at, expires_at)
		 VALUES (?, ?, ?, ?)`,
		session.ID, session.SteamID, session.CreatedAt, session.ExpiresAt,
	)
	return err
}

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

func (s *SQLiteStore) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

func (s *SQLiteStore) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now())
	return err
}

func (s *SQLiteStore) CreateMatch(ctx context.Context, match *Match) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO matches (id, dota_match_id, state, started_at, ended_at, winner, duration)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		match.ID, match.DotaMatchID, match.State, match.StartedAt, match.EndedAt, match.Winner, match.Duration,
	)
	return err
}

func (s *SQLiteStore) UpdateMatch(ctx context.Context, match *Match) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE matches SET dota_match_id = ?, state = ?, ended_at = ?, winner = ?, duration = ?
		 WHERE id = ?`,
		match.DotaMatchID, match.State, match.EndedAt, match.Winner, match.Duration, match.ID,
	)
	return err
}

func (s *SQLiteStore) GetMatch(ctx context.Context, matchID string) (*Match, error) {
	var match Match
	err := s.db.QueryRowContext(ctx,
		`SELECT id, dota_match_id, state, started_at, ended_at, winner, duration
		 FROM matches WHERE id = ?`, matchID).Scan(
		&match.ID, &match.DotaMatchID, &match.State,
		&match.StartedAt, &match.EndedAt, &match.Winner, &match.Duration,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &match, nil
}

func (s *SQLiteStore) SetMatchWinner(ctx context.Context, matchID string, winner string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE matches SET winner = ? WHERE id = ?`,
		winner, matchID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("match not found")
	}
	return nil
}

func (s *SQLiteStore) AddMatchPlayer(ctx context.Context, mp *MatchPlayer) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO match_players (match_id, steam_id, team, was_captain, accepted)
		 VALUES (?, ?, ?, ?, ?)`,
		mp.MatchID, mp.SteamID, mp.Team, mp.WasCaptain, mp.Accepted,
	)
	return err
}

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

func (s *SQLiteStore) ListMatches(ctx context.Context, limit int) ([]Match, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, dota_match_id, state, started_at, ended_at, winner, duration
		 FROM matches
		 WHERE state = 'completed'
		 ORDER BY ended_at DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []Match
	for rows.Next() {
		var m Match
		if err := rows.Scan(&m.ID, &m.DotaMatchID, &m.State, &m.StartedAt, &m.EndedAt, &m.Winner, &m.Duration); err != nil {
			return nil, err
		}
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

func (s *SQLiteStore) GetLeaderboard(ctx context.Context, startDate, endDate *time.Time) ([]LeaderboardEntry, error) {
	query := `
		SELECT
			mp.steam_id,
			u.name,
			u.avatar_url,
			COUNT(*) as total,
			SUM(CASE WHEN m.winner = mp.team THEN 1 ELSE 0 END) as wins,
			SUM(CASE WHEN m.winner IS NOT NULL AND m.winner != mp.team THEN 1 ELSE 0 END) as losses
		FROM match_players mp
		JOIN matches m ON mp.match_id = m.id
		LEFT JOIN users u ON mp.steam_id = u.steam_id
		WHERE m.state = 'completed' AND m.winner IS NOT NULL
	`
	args := []interface{}{}

	if startDate != nil {
		query += " AND m.ended_at >= ?"
		args = append(args, *startDate)
	}
	if endDate != nil {
		query += " AND m.ended_at <= ?"
		args = append(args, *endDate)
	}

	query += `
		GROUP BY mp.steam_id
		ORDER BY (wins - losses) DESC, total DESC
	`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LeaderboardEntry
	for rows.Next() {
		var e LeaderboardEntry
		var name, avatar sql.NullString
		if err := rows.Scan(&e.SteamID, &name, &avatar, &e.Total, &e.Wins, &e.Losses); err != nil {
			return nil, err
		}
		e.Name = name.String
		if e.Name == "" {
			e.Name = e.SteamID
		}
		e.AvatarURL = avatar.String
		if e.Total > 0 {
			e.WinRate = float64(e.Wins) / float64(e.Total) * 100
		}
		entries = append(entries, e)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range entries {
		entries[i].Streak = s.calculateStreak(ctx, entries[i].SteamID, startDate, endDate)
	}

	return entries, nil
}

func (s *SQLiteStore) calculateStreak(ctx context.Context, steamID string, startDate, endDate *time.Time) int {
	query := `
		SELECT
			CASE WHEN m.winner = mp.team THEN 1 ELSE -1 END as result
		FROM match_players mp
		JOIN matches m ON mp.match_id = m.id
		WHERE mp.steam_id = ? AND m.state = 'completed' AND m.winner IS NOT NULL
	`
	args := []interface{}{steamID}

	if startDate != nil {
		query += " AND m.ended_at >= ?"
		args = append(args, *startDate)
	}
	if endDate != nil {
		query += " AND m.ended_at <= ?"
		args = append(args, *endDate)
	}

	query += " ORDER BY m.ended_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0
	}
	defer rows.Close()

	streak := 0
	var firstResult int
	first := true

	for rows.Next() {
		var result int
		if err := rows.Scan(&result); err != nil {
			return 0
		}

		if first {
			firstResult = result
			streak = result
			first = false
		} else if result == firstResult {
			streak += result
		} else {
			break // Streak ended
		}
	}

	return streak
}

func (s *SQLiteStore) ListMatchesWithPlayers(ctx context.Context, limit int) ([]MatchWithPlayers, error) {
	matches, err := s.ListMatches(ctx, limit)
	if err != nil {
		return nil, err
	}

	result := make([]MatchWithPlayers, 0, len(matches))
	for _, m := range matches {
		mwp := MatchWithPlayers{Match: m}

		rows, err := s.db.QueryContext(ctx,
			`SELECT mp.steam_id, u.name, u.avatar_url, mp.team, mp.was_captain
			 FROM match_players mp
			 LEFT JOIN users u ON mp.steam_id = u.steam_id
			 WHERE mp.match_id = ?`, m.ID)
		if err != nil {
			return nil, err
		}

		for rows.Next() {
			var p MatchPlayerInfo
			var name, avatar sql.NullString
			if err := rows.Scan(&p.SteamID, &name, &avatar, &p.Team, &p.WasCaptain); err != nil {
				rows.Close()
				return nil, err
			}
			p.Name = name.String
			p.AvatarURL = avatar.String
			if p.Name == "" {
				p.Name = p.SteamID // Fallback to Steam ID if no name
			}

			if p.Team == "radiant" {
				mwp.Radiant = append(mwp.Radiant, p)
				if p.WasCaptain {
					captain := p
					mwp.RadiantCaptain = &captain
				}
			} else {
				mwp.Dire = append(mwp.Dire, p)
				if p.WasCaptain {
					captain := p
					mwp.DireCaptain = &captain
				}
			}
		}
		rows.Close()

		result = append(result, mwp)
	}

	return result, nil
}

// Push Subscription methods

func (s *SQLiteStore) SavePushSubscription(ctx context.Context, sub *PushSubscription) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO push_subscriptions (steam_id, endpoint, p256dh, auth)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(endpoint) DO UPDATE SET
		 steam_id = excluded.steam_id,
		 p256dh = excluded.p256dh,
		 auth = excluded.auth`,
		sub.SteamID, sub.Endpoint, sub.P256dh, sub.Auth,
	)
	return err
}

func (s *SQLiteStore) GetPushSubscriptions(ctx context.Context, steamID string) ([]PushSubscription, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, steam_id, endpoint, p256dh, auth, created_at
		 FROM push_subscriptions WHERE steam_id = ?`,
		steamID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []PushSubscription
	for rows.Next() {
		var sub PushSubscription
		if err := rows.Scan(&sub.ID, &sub.SteamID, &sub.Endpoint, &sub.P256dh, &sub.Auth, &sub.CreatedAt); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}

	return subs, rows.Err()
}

func (s *SQLiteStore) GetAllPushSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, steam_id, endpoint, p256dh, auth, created_at
		 FROM push_subscriptions`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []PushSubscription
	for rows.Next() {
		var sub PushSubscription
		if err := rows.Scan(&sub.ID, &sub.SteamID, &sub.Endpoint, &sub.P256dh, &sub.Auth, &sub.CreatedAt); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}

	return subs, rows.Err()
}

func (s *SQLiteStore) DeletePushSubscription(ctx context.Context, endpoint string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}
