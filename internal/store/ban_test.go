package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestUserIsBanned(t *testing.T) {
	now := time.Date(2026, 10, 18, 20, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	indefinite := IndefiniteBan

	tests := []struct {
		name           string
		bannedUntil    *time.Time
		wantBanned     bool
		wantIndefinite bool
	}{
		{"never banned", nil, false, false},
		{"expired ban", &past, false, false},
		{"active timed ban", &future, true, false},
		{"indefinite ban", &indefinite, true, true},
	}

	for _, tt := range tests {
		u := User{BannedUntil: tt.bannedUntil}
		if got := u.IsBanned(now); got != tt.wantBanned {
			t.Errorf("%s: IsBanned = %v, want %v", tt.name, got, tt.wantBanned)
		}
		if got := u.IsBannedIndefinitely(); got != tt.wantIndefinite {
			t.Errorf("%s: IsBannedIndefinitely = %v, want %v", tt.name, got, tt.wantIndefinite)
		}
	}
}

func TestSetBannedUntilRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now()
	user := &User{SteamID: "76561198000000001", Name: "Banned Guy", CaptainPriority: 5, CreatedAt: now, UpdatedAt: now}
	if err := s.UpsertUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	until := now.Add(24 * time.Hour).Truncate(time.Second)
	if err := s.SetBannedUntil(ctx, user.SteamID, &until); err != nil {
		t.Fatal(err)
	}

	if err := s.UpsertUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetUser(ctx, user.SteamID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BannedUntil == nil || !got.BannedUntil.Equal(until) {
		t.Fatalf("GetUser BannedUntil = %v, want %v", got.BannedUntil, until)
	}

	users, err := s.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].BannedUntil == nil || !users[0].BannedUntil.Equal(until) {
		t.Fatalf("ListUsers BannedUntil = %+v, want %v", users, until)
	}

	indefinite := IndefiniteBan
	if err := s.SetBannedUntil(ctx, user.SteamID, &indefinite); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetUser(ctx, user.SteamID)
	if !got.IsBannedIndefinitely() {
		t.Fatalf("expected indefinite ban, got %v", got.BannedUntil)
	}

	if err := s.SetBannedUntil(ctx, user.SteamID, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetUser(ctx, user.SteamID)
	if got.BannedUntil != nil {
		t.Fatalf("expected ban cleared, got %v", got.BannedUntil)
	}

	if err := s.SetBannedUntil(ctx, "unknown", nil); err == nil {
		t.Fatal("expected error for unknown user")
	}
}
