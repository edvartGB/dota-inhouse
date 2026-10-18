package web

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/edvart/dota-inhouse/internal/store"
)

func TestBanExpiry(t *testing.T) {
	now := time.Date(2026, 10, 18, 20, 0, 0, 0, time.UTC)

	got, err := banExpiry("7d", now)
	if err != nil || !got.Equal(now.Add(7*24*time.Hour)) {
		t.Errorf("7d: got %v, %v", got, err)
	}

	got, err = banExpiry("indefinite", now)
	if err != nil || !got.Equal(store.IndefiniteBan) {
		t.Errorf("indefinite: got %v, %v", got, err)
	}

	for _, bad := range []string{"", "2d", "permanent", "-1d"} {
		if _, err := banExpiry(bad, now); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestBanMessage(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	indefinite := store.IndefiniteBan

	if msg := banMessage(&store.User{}, now); msg != "" {
		t.Errorf("unbanned: got %q", msg)
	}
	if msg := banMessage(&store.User{BannedUntil: &past}, now); msg != "" {
		t.Errorf("expired: got %q", msg)
	}
	if msg := banMessage(&store.User{BannedUntil: &future}, now); !strings.HasPrefix(msg, "You are banned until ") {
		t.Errorf("timed: got %q", msg)
	}
	if msg := banMessage(&store.User{BannedUntil: &indefinite}, now); msg != "You are banned indefinitely" {
		t.Errorf("indefinite: got %q", msg)
	}
}

func TestAdminPlayersPageRendersBanState(t *testing.T) {
	tmpl, err := LoadTemplatesFromDir("../../web/templates")
	if err != nil {
		t.Fatalf("loading templates: %v", err)
	}

	now := time.Now()
	timed := now.Add(48 * time.Hour)
	indefinite := store.IndefiniteBan
	data := map[string]interface{}{
		"Users": []store.User{
			{SteamID: "1", Name: "Free", CaptainPriority: 5},
			{SteamID: "2", Name: "Timed", CaptainPriority: 5, BannedUntil: &timed},
			{SteamID: "3", Name: "Forever", CaptainPriority: 5, BannedUntil: &indefinite},
		},
		"SortBy":  "name",
		"SortDir": "asc",
		"Now":     now,
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "admin-players.html", data); err != nil {
		t.Fatalf("rendering admin-players.html: %v", err)
	}
	html := buf.String()

	if got := strings.Count(html, `class="ban-select"`); got != 1 {
		t.Errorf("got %d ban selects, want 1 (only the unbanned player)", got)
	}
	if got := strings.Count(html, ">Unban<"); got != 2 {
		t.Errorf("got %d unban buttons, want 2", got)
	}
	if !strings.Contains(html, "Banned until "+timed.Local().Format("2 Jan 15:04")) {
		t.Error("missing timed ban expiry")
	}
	if !strings.Contains(html, "Banned indefinitely") {
		t.Error("missing indefinite ban label")
	}
}
