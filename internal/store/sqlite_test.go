package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func upsertTestUser(t *testing.T, st Store, id, name string, priority int) {
	t.Helper()
	now := time.Now()
	err := st.UpsertUser(context.Background(), &User{
		SteamID:         id,
		Name:            name,
		CaptainPriority: priority,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		t.Fatalf("failed to upsert user %s: %v", id, err)
	}
}

func TestUserUpsertGetAndUpdatePriority(t *testing.T) {
	st := newTestSQLiteStore(t)
	ctx := context.Background()

	now := time.Now()
	if err := st.UpsertUser(ctx, &User{
		SteamID:         "u1",
		Name:            "Alice",
		AvatarURL:       "avatar1",
		CaptainPriority: 5,
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("upsert user failed: %v", err)
	}

	u, err := st.GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("get user failed: %v", err)
	}
	if u == nil || u.Name != "Alice" || u.CaptainPriority != 5 {
		t.Fatalf("unexpected user after insert: %+v", u)
	}

	if err := st.UpsertUser(ctx, &User{
		SteamID:         "u1",
		Name:            "Alice Updated",
		AvatarURL:       "avatar2",
		CaptainPriority: 9,
		CreatedAt:       now,
		UpdatedAt:       now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("second upsert failed: %v", err)
	}

	u, err = st.GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("get updated user failed: %v", err)
	}
	if u.Name != "Alice Updated" {
		t.Fatalf("expected updated name, got %q", u.Name)
	}
	// Upsert intentionally does not overwrite captain_priority.
	if u.CaptainPriority != 5 {
		t.Fatalf("expected captain priority to remain 5, got %d", u.CaptainPriority)
	}

	if err := st.UpdateCaptainPriority(ctx, "u1", 8); err != nil {
		t.Fatalf("update captain priority failed: %v", err)
	}
	u, err = st.GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("get user after priority update failed: %v", err)
	}
	if u.CaptainPriority != 8 {
		t.Fatalf("expected captain priority 8, got %d", u.CaptainPriority)
	}
}

func TestSessionLifecycleAndExpiry(t *testing.T) {
	st := newTestSQLiteStore(t)
	ctx := context.Background()
	upsertTestUser(t, st, "u1", "Alice", 5)

	expired := &Session{
		ID:        "expired",
		SteamID:   "u1",
		CreatedAt: time.Now().Add(-48 * time.Hour),
		ExpiresAt: time.Now().Add(-24 * time.Hour),
	}
	active := &Session{
		ID:        "active",
		SteamID:   "u1",
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	if err := st.CreateSession(ctx, expired); err != nil {
		t.Fatalf("create expired session failed: %v", err)
	}
	if err := st.CreateSession(ctx, active); err != nil {
		t.Fatalf("create active session failed: %v", err)
	}

	got, err := st.GetSession(ctx, "expired")
	if err != nil {
		t.Fatalf("get expired session failed: %v", err)
	}
	if got != nil {
		t.Fatal("expected expired session to be filtered out")
	}

	got, err = st.GetSession(ctx, "active")
	if err != nil {
		t.Fatalf("get active session failed: %v", err)
	}
	if got == nil || got.SteamID != "u1" {
		t.Fatalf("unexpected active session: %+v", got)
	}

	if err := st.DeleteExpiredSessions(ctx); err != nil {
		t.Fatalf("delete expired sessions failed: %v", err)
	}
}

func TestMatchHistoryAndPlayers(t *testing.T) {
	st := newTestSQLiteStore(t)
	ctx := context.Background()
	upsertTestUser(t, st, "u1", "Alice", 5)
	upsertTestUser(t, st, "u2", "Bob", 5)

	winner := "radiant"
	endedAt := time.Now()
	match := &Match{
		ID:          "m1",
		DotaMatchID: 123,
		State:       "completed",
		StartedAt:   endedAt.Add(-40 * time.Minute),
		EndedAt:     &endedAt,
		Winner:      &winner,
	}
	if err := st.CreateMatch(ctx, match); err != nil {
		t.Fatalf("create match failed: %v", err)
	}

	if err := st.AddMatchPlayer(ctx, &MatchPlayer{
		MatchID:    "m1",
		SteamID:    "u1",
		Team:       "radiant",
		WasCaptain: true,
		Accepted:   true,
	}); err != nil {
		t.Fatalf("add match player u1 failed: %v", err)
	}
	if err := st.AddMatchPlayer(ctx, &MatchPlayer{
		MatchID:    "m1",
		SteamID:    "u2",
		Team:       "dire",
		WasCaptain: true,
		Accepted:   true,
	}); err != nil {
		t.Fatalf("add match player u2 failed: %v", err)
	}

	players, err := st.GetMatchPlayers(ctx, "m1")
	if err != nil {
		t.Fatalf("get match players failed: %v", err)
	}
	if len(players) != 2 {
		t.Fatalf("expected 2 match players, got %d", len(players))
	}

	matches, err := st.ListMatches(ctx, 10)
	if err != nil {
		t.Fatalf("list matches failed: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 completed match, got %d", len(matches))
	}

	withPlayers, err := st.ListMatchesWithPlayers(ctx, 10)
	if err != nil {
		t.Fatalf("list matches with players failed: %v", err)
	}
	if len(withPlayers) != 1 {
		t.Fatalf("expected 1 match with players, got %d", len(withPlayers))
	}
	if withPlayers[0].RadiantCaptain == nil || withPlayers[0].RadiantCaptain.SteamID != "u1" {
		t.Fatalf("unexpected radiant captain: %+v", withPlayers[0].RadiantCaptain)
	}
	if withPlayers[0].DireCaptain == nil || withPlayers[0].DireCaptain.SteamID != "u2" {
		t.Fatalf("unexpected dire captain: %+v", withPlayers[0].DireCaptain)
	}
}

func TestLeaderboardCalculations(t *testing.T) {
	st := newTestSQLiteStore(t)
	ctx := context.Background()
	upsertTestUser(t, st, "u1", "Alice", 5)
	upsertTestUser(t, st, "u2", "Bob", 5)

	makeMatch := func(id string, endedAt time.Time, winner string) {
		w := winner
		err := st.CreateMatch(ctx, &Match{
			ID:        id,
			State:     "completed",
			StartedAt: endedAt.Add(-30 * time.Minute),
			EndedAt:   &endedAt,
			Winner:    &w,
		})
		if err != nil {
			t.Fatalf("create match %s failed: %v", id, err)
		}
		if err := st.AddMatchPlayer(ctx, &MatchPlayer{MatchID: id, SteamID: "u1", Team: "radiant", Accepted: true}); err != nil {
			t.Fatalf("add u1 player for %s failed: %v", id, err)
		}
		if err := st.AddMatchPlayer(ctx, &MatchPlayer{MatchID: id, SteamID: "u2", Team: "dire", Accepted: true}); err != nil {
			t.Fatalf("add u2 player for %s failed: %v", id, err)
		}
	}

	base := time.Now().Add(-3 * time.Hour)
	makeMatch("m1", base, "radiant")             // u1 win, u2 loss
	makeMatch("m2", base.Add(1*time.Hour), "dire") // u1 loss, u2 win
	makeMatch("m3", base.Add(2*time.Hour), "dire") // u1 loss, u2 win

	entries, err := st.GetLeaderboard(ctx, nil, nil)
	if err != nil {
		t.Fatalf("get leaderboard failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 leaderboard entries, got %d", len(entries))
	}

	byID := make(map[string]LeaderboardEntry)
	for _, e := range entries {
		byID[e.SteamID] = e
	}

	u1 := byID["u1"]
	if u1.Wins != 1 || u1.Losses != 2 || u1.Total != 3 {
		t.Fatalf("unexpected u1 stats: %+v", u1)
	}
	if u1.Streak != -2 {
		t.Fatalf("expected u1 streak -2, got %d", u1.Streak)
	}

	u2 := byID["u2"]
	if u2.Wins != 2 || u2.Losses != 1 || u2.Total != 3 {
		t.Fatalf("unexpected u2 stats: %+v", u2)
	}
	if u2.Streak != 2 {
		t.Fatalf("expected u2 streak 2, got %d", u2.Streak)
	}
}

func TestPushSubscriptionsCRUD(t *testing.T) {
	st := newTestSQLiteStore(t)
	ctx := context.Background()
	upsertTestUser(t, st, "u1", "Alice", 5)
	upsertTestUser(t, st, "u2", "Bob", 5)

	if err := st.SavePushSubscription(ctx, &PushSubscription{
		SteamID:  "u1",
		Endpoint: "endpoint-1",
		P256dh:   "k1",
		Auth:     "a1",
	}); err != nil {
		t.Fatalf("save first push subscription failed: %v", err)
	}

	// Upsert same endpoint with new keys.
	if err := st.SavePushSubscription(ctx, &PushSubscription{
		SteamID:  "u1",
		Endpoint: "endpoint-1",
		P256dh:   "k1-updated",
		Auth:     "a1-updated",
	}); err != nil {
		t.Fatalf("update push subscription failed: %v", err)
	}

	if err := st.SavePushSubscription(ctx, &PushSubscription{
		SteamID:  "u2",
		Endpoint: "endpoint-2",
		P256dh:   "k2",
		Auth:     "a2",
	}); err != nil {
		t.Fatalf("save second push subscription failed: %v", err)
	}

	subsU1, err := st.GetPushSubscriptions(ctx, "u1")
	if err != nil {
		t.Fatalf("get push subscriptions for u1 failed: %v", err)
	}
	if len(subsU1) != 1 {
		t.Fatalf("expected 1 subscription for u1, got %d", len(subsU1))
	}
	if subsU1[0].P256dh != "k1-updated" || subsU1[0].Auth != "a1-updated" {
		t.Fatalf("expected updated subscription keys, got %+v", subsU1[0])
	}

	allSubs, err := st.GetAllPushSubscriptions(ctx)
	if err != nil {
		t.Fatalf("get all subscriptions failed: %v", err)
	}
	if len(allSubs) != 2 {
		t.Fatalf("expected 2 total subscriptions, got %d", len(allSubs))
	}

	if err := st.DeletePushSubscription(ctx, "endpoint-1"); err != nil {
		t.Fatalf("delete push subscription failed: %v", err)
	}
	subsU1, err = st.GetPushSubscriptions(ctx, "u1")
	if err != nil {
		t.Fatalf("get push subscriptions after delete failed: %v", err)
	}
	if len(subsU1) != 0 {
		t.Fatalf("expected 0 subscriptions for u1 after delete, got %d", len(subsU1))
	}
}

func TestSetMatchWinnerNotFound(t *testing.T) {
	st := newTestSQLiteStore(t)
	err := st.SetMatchWinner(context.Background(), "does-not-exist", "radiant")
	if err == nil {
		t.Fatal("expected error when setting winner for non-existent match")
	}
}

