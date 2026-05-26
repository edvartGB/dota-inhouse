package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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

	// SQLite permits only one writer at a time. Keeping one pooled connection
	// avoids in-process write lock races between recorder, queue, and session writes.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	pragmas := []struct {
		name string
		stmt string
	}{
		{name: "foreign keys", stmt: "PRAGMA foreign_keys = ON"},
		{name: "busy timeout", stmt: "PRAGMA busy_timeout = 5000"},
		{name: "WAL journal mode", stmt: "PRAGMA journal_mode = WAL"},
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma.stmt); err != nil {
			return nil, fmt.Errorf("failed to configure SQLite %s: %w", pragma.name, err)
		}
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
		`CREATE INDEX IF NOT EXISTS idx_matches_state_ended ON matches(state, ended_at DESC)`,
		`CREATE TABLE IF NOT EXISTS match_players (
			match_id TEXT NOT NULL REFERENCES matches(id),
			steam_id TEXT NOT NULL REFERENCES users(steam_id),
			team TEXT NOT NULL,
			was_captain INTEGER DEFAULT 0,
			accepted INTEGER DEFAULT 0,
			PRIMARY KEY (match_id, steam_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_match_players_steam ON match_players(steam_id)`,
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

func retrySQLiteBusy(ctx context.Context, fn func() error) error {
	delays := []time.Duration{0, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second}
	var lastErr error
	for i, delay := range delays {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		err := fn()
		if err == nil {
			return nil
		}
		if !isSQLiteBusy(err) || i == len(delays)-1 {
			return err
		}
		lastErr = err
	}
	return lastErr
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") ||
		strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked")
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
		 	avatar_url = excluded.avatar_url,
		 	updated_at = excluded.updated_at`,
		user.SteamID, user.Name, user.AvatarURL,
		user.CaptainPriority, user.CreatedAt, user.UpdatedAt,
	)
	return err
}

func (s *SQLiteStore) UpdateUserProfile(ctx context.Context, steamID, displayName string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE users
		 SET name = ?, updated_at = ?
		 WHERE steam_id = ?`,
		displayName, time.Now(), steamID,
	)
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

func (s *SQLiteStore) CreateMatchWithPlayers(ctx context.Context, match *Match, players []MatchPlayer) error {
	return retrySQLiteBusy(ctx, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO matches (id, dota_match_id, state, started_at, ended_at, winner, duration)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			match.ID, match.DotaMatchID, match.State, match.StartedAt, match.EndedAt, match.Winner, match.Duration,
		); err != nil {
			return err
		}

		for _, player := range players {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO match_players (match_id, steam_id, team, was_captain, accepted)
				 VALUES (?, ?, ?, ?, ?)`,
				player.MatchID, player.SteamID, player.Team, player.WasCaptain, player.Accepted,
			); err != nil {
				return err
			}
		}

		return tx.Commit()
	})
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

func (s *SQLiteStore) AddMatchPlayer(ctx context.Context, mp *MatchPlayer) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO match_players (match_id, steam_id, team, was_captain, accepted)
		 VALUES (?, ?, ?, ?, ?)`,
		mp.MatchID, mp.SteamID, mp.Team, mp.WasCaptain, mp.Accepted,
	)
	return err
}

func (s *SQLiteStore) ListMatches(ctx context.Context, limit int) ([]Match, error) {
	return s.ListMatchesPage(ctx, limit, 0)
}

func (s *SQLiteStore) ListMatchesPage(ctx context.Context, limit, offset int) ([]Match, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, dota_match_id, state, started_at, ended_at, winner, duration
		 FROM matches
		 WHERE state = 'completed'
		 ORDER BY ended_at DESC
		 LIMIT ? OFFSET ?`, limit, offset)
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

func (s *SQLiteStore) CountCompletedMatches(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM matches WHERE state = 'completed'`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *SQLiteStore) ListIncompleteMatches(ctx context.Context, limit int) ([]Match, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, dota_match_id, state, started_at, ended_at, winner, duration
		 FROM matches
		 WHERE state != 'completed'
		 ORDER BY started_at DESC
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
		e.Delta = e.Wins - e.Losses
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
	return s.ListMatchesWithPlayersPage(ctx, limit, 0)
}

func (s *SQLiteStore) ListMatchesWithPlayersPage(ctx context.Context, limit, offset int) ([]MatchWithPlayers, error) {
	matches, err := s.ListMatchesPage(ctx, limit, offset)
	if err != nil {
		return nil, err
	}

	return s.buildMatchesWithPlayers(ctx, matches)
}

func (s *SQLiteStore) buildMatchesWithPlayers(ctx context.Context, matches []Match) ([]MatchWithPlayers, error) {
	result := make([]MatchWithPlayers, len(matches))
	if len(matches) == 0 {
		return result, nil
	}

	matchIndex := make(map[string]int, len(matches))
	placeholders := make([]string, len(matches))
	args := make([]interface{}, len(matches))
	for i, m := range matches {
		result[i] = MatchWithPlayers{Match: m}
		matchIndex[m.ID] = i
		placeholders[i] = "?"
		args[i] = m.ID
	}

	query := fmt.Sprintf(`SELECT mp.match_id, mp.steam_id, u.name, u.avatar_url, mp.team, mp.was_captain
		 FROM match_players mp
		 LEFT JOIN users u ON mp.steam_id = u.steam_id
		 WHERE mp.match_id IN (%s)
		 ORDER BY mp.match_id, mp.team DESC, mp.was_captain DESC`, strings.Join(placeholders, ","))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var matchID string
		var p MatchPlayerInfo
		var name, avatar sql.NullString
		if err := rows.Scan(&matchID, &p.SteamID, &name, &avatar, &p.Team, &p.WasCaptain); err != nil {
			return nil, err
		}
		p.Name = name.String
		p.AvatarURL = avatar.String
		if p.Name == "" {
			p.Name = p.SteamID
		}

		idx, ok := matchIndex[matchID]
		if !ok {
			continue
		}
		mwp := &result[idx]
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

	return result, rows.Err()
}

func (s *SQLiteStore) ListIncompleteMatchesWithPlayers(ctx context.Context, limit int) ([]MatchWithPlayers, error) {
	matches, err := s.ListIncompleteMatches(ctx, limit)
	if err != nil {
		return nil, err
	}
	return s.buildMatchesWithPlayers(ctx, matches)
}

// Push Subscription methods

func (s *SQLiteStore) SavePushSubscription(ctx context.Context, sub *PushSubscription) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO push_subscriptions (steam_id, endpoint, p256dh, auth)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(endpoint) DO UPDATE SET
		 steam_id = excluded.steam_id,
		 p256dh = excluded.p256dh,
		 auth = excluded.auth,
		 created_at = CURRENT_TIMESTAMP`,
		sub.SteamID, sub.Endpoint, sub.P256dh, sub.Auth,
	)
	if err != nil {
		return err
	}

	// Keep only the most recent subscriptions per user to avoid stale endpoint buildup.
	_, err = s.db.ExecContext(ctx,
		`DELETE FROM push_subscriptions
		 WHERE steam_id = ?
		   AND id NOT IN (
			 SELECT id
			 FROM push_subscriptions
			 WHERE steam_id = ?
			 ORDER BY created_at DESC, id DESC
			 LIMIT 3
		   )`,
		sub.SteamID, sub.SteamID,
	)
	return err
}

func (s *SQLiteStore) GetPushSubscriptions(ctx context.Context, steamID string) ([]PushSubscription, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, steam_id, endpoint, p256dh, auth, created_at
		 FROM push_subscriptions
		 WHERE steam_id = ?
		 ORDER BY created_at DESC, id DESC`,
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

func (s *SQLiteStore) DeletePushSubscription(ctx context.Context, endpoint string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}
