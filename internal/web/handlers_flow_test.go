package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/edvart/dota-inhouse/internal/store"
)

func (e *testEnv) expect(method, path string, cookie *http.Cookie, form url.Values, wantCode int, wantBody string) {
	e.t.Helper()
	rec := e.request(method, path, cookie, form)
	if rec.Code != wantCode {
		e.t.Errorf("%s %s: status %d, want %d (body %q)", method, path, rec.Code, wantCode, rec.Body.String())
		return
	}
	if wantBody != "" && !strings.Contains(rec.Body.String(), wantBody) {
		e.t.Errorf("%s %s: body %q does not contain %q", method, path, rec.Body.String(), wantBody)
	}
}

func (e *testEnv) queueIDs() []string {
	queue, _, _, _ := e.coord.GetState()
	ids := make([]string, 0, len(queue))
	for _, p := range queue {
		ids = append(ids, p.SteamID)
	}
	return ids
}

func TestQueueHandlers(t *testing.T) {
	e := newTestEnv(t)
	e.addPlayers(2)
	e.addUser(testAdminID, "AdminUser", 1)
	p1 := e.login(testSteamID(1))
	admin := e.login(testAdminID)

	e.expect(http.MethodPost, "/queue/join", p1, nil, http.StatusNoContent, "")
	e.expect(http.MethodPost, "/queue/join", p1, nil, http.StatusBadRequest, "already in queue")
	if ids := e.queueIDs(); len(ids) != 1 || ids[0] != testSteamID(1) {
		t.Errorf("queue = %v", ids)
	}
	e.expect(http.MethodPost, "/queue/leave", p1, nil, http.StatusNoContent, "")
	e.expect(http.MethodPost, "/queue/leave", p1, nil, http.StatusBadRequest, "not in queue")

	e.expect(http.MethodPost, "/admin/queue/close", admin, nil, http.StatusNoContent, "")
	e.expect(http.MethodPost, "/queue/join", p1, nil, http.StatusBadRequest, "queue is closed")
	e.expect(http.MethodPost, "/admin/queue/open", admin, nil, http.StatusNoContent, "")
	e.expect(http.MethodPost, "/admin/queue/bogus", admin, nil, http.StatusBadRequest, "status must be")
	e.expect(http.MethodPost, "/queue/join", p1, nil, http.StatusNoContent, "")

	until := store.IndefiniteBan
	if err := e.store.SetBannedUntil(context.Background(), testSteamID(2), &until); err != nil {
		t.Fatal(err)
	}
	e.expect(http.MethodPost, "/queue/join", e.login(testSteamID(2)), nil, http.StatusForbidden, "You are banned indefinitely")
}

func TestMatchHandlers(t *testing.T) {
	e := newTestEnv(t)
	e.addPlayers(11)
	for i := 1; i <= 10; i++ {
		e.expect(http.MethodPost, "/queue/join", e.login(testSteamID(i)), nil, http.StatusNoContent, "")
	}
	m := e.currentMatch()
	outsider := e.login(testSteamID(11))

	e.expect(http.MethodPost, "/match/"+m.ID+"/accept", outsider, nil, http.StatusBadRequest, "player not in this match")
	e.expect(http.MethodPost, "/match/nope/accept", e.login(testSteamID(1)), nil, http.StatusBadRequest, "match not found")
	for i := 1; i <= 10; i++ {
		e.expect(http.MethodPost, "/match/"+m.ID+"/accept", e.login(testSteamID(i)), nil, http.StatusNoContent, "")
	}

	m = e.currentMatch()
	if m.State != coordinator.MatchStateChoosingSide {
		t.Fatalf("state = %v, want choosing side", m.State)
	}
	chooser := m.Captains[m.SideChooserIndex].SteamID
	other := m.Captains[1-m.SideChooserIndex].SteamID
	if chooser != testSteamID(2) || other != testSteamID(1) {
		t.Errorf("captains chooser=%s other=%s, want lower priority to choose", chooser, other)
	}
	e.expect(http.MethodPost, "/match/"+m.ID+"/side/radiant", e.login(other), nil, http.StatusBadRequest, "not your side choice")
	e.expect(http.MethodPost, "/match/"+m.ID+"/side/middle", e.login(chooser), nil, http.StatusBadRequest, "side must be")
	e.expect(http.MethodPost, "/match/"+m.ID+"/side/dire", e.login(chooser), nil, http.StatusNoContent, "")

	m = e.currentMatch()
	if m.Captains[1].SteamID != chooser {
		t.Errorf("chooser picked dire but captains = %s/%s", m.Captains[0].SteamID, m.Captains[1].SteamID)
	}
	picker := m.Captains[m.CurrentPicker].SteamID
	waiter := m.Captains[1-m.CurrentPicker].SteamID
	target := m.AvailablePlayers[0].SteamID
	e.expect(http.MethodPost, "/match/"+m.ID+"/pick/"+target, e.login(waiter), nil, http.StatusBadRequest, "not your turn")
	e.expect(http.MethodPost, "/match/"+m.ID+"/pick/"+testSteamID(11), e.login(picker), nil, http.StatusBadRequest, "not available")
	e.expect(http.MethodPost, "/match/"+m.ID+"/pick/"+target, e.login(picker), nil, http.StatusNoContent, "")

	m = e.currentMatch()
	if m.PickCount != 1 || len(m.Radiant) != 2 {
		t.Errorf("after first pick: pickCount=%d radiant=%d", m.PickCount, len(m.Radiant))
	}
	e.expect(http.MethodPost, "/queue/leave", e.login(testSteamID(3)), nil, http.StatusBadRequest, "cannot leave queue while in a match")
}

func TestAdminMatchHandlers(t *testing.T) {
	e := newTestEnv(t)
	users := e.addPlayers(10)
	e.addUser(testAdminID, "AdminUser", 1)
	admin := e.login(testAdminID)
	m := e.matchWaitingForBot(users)
	base := "/admin/match/" + m.ID

	e.expect(http.MethodPost, base+"/lobby-timer/resume", admin, nil, http.StatusBadRequest, "not paused")
	e.expect(http.MethodPost, base+"/lobby-timer/pause", admin, nil, http.StatusNoContent, "")
	e.expect(http.MethodPost, base+"/lobby-timer/pause", admin, nil, http.StatusBadRequest, "already paused")
	if !e.currentMatch().LobbyPaused {
		t.Errorf("match not paused")
	}
	e.expect(http.MethodPost, base+"/lobby-timer/resume", admin, nil, http.StatusNoContent, "")
	e.expect(http.MethodPost, "/admin/match/nope/lobby-timer/pause", admin, nil, http.StatusBadRequest, "match not found")
	e.expect(http.MethodPost, "/admin/match/nope/cancel", admin, nil, http.StatusBadRequest, "match not found")
	e.expect(http.MethodPost, base+"/result/nobody", admin, nil, http.StatusBadRequest, "winner must be")

	e.coord.Send(coordinator.BotGameStarted{MatchID: m.ID, DotaMatchID: 8200000001})
	e.sync()
	var players []store.MatchPlayer
	for _, p := range m.Radiant {
		players = append(players, store.MatchPlayer{MatchID: m.ID, SteamID: p.SteamID, Team: "radiant"})
	}
	for _, p := range m.Dire {
		players = append(players, store.MatchPlayer{MatchID: m.ID, SteamID: p.SteamID, Team: "dire"})
	}
	err := e.store.CreateMatchWithPlayers(context.Background(), &store.Match{ID: m.ID, DotaMatchID: 8200000001, State: "in_progress", StartedAt: time.Now()}, players)
	if err != nil {
		t.Fatal(err)
	}

	e.expect(http.MethodPost, base+"/result/radiant", admin, nil, http.StatusNoContent, "")
	stored, err := e.store.GetMatch(context.Background(), m.ID)
	if err != nil || stored == nil {
		t.Fatalf("stored match: %v %v", stored, err)
	}
	if stored.State != "completed" || stored.Winner == nil || *stored.Winner != "radiant" {
		t.Errorf("stored match state=%s winner=%v", stored.State, stored.Winner)
	}
	if _, matches, _, _ := e.coord.GetState(); len(matches) != 0 {
		t.Errorf("active matches = %d, want 0", len(matches))
	}
}

func TestAdminCancelHandlers(t *testing.T) {
	for _, tc := range []struct {
		query     string
		wantQueue int
	}{
		{"", 10},
		{"?return=true", 10},
		{"?return=false", 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			e := newTestEnv(t)
			users := e.addPlayers(10)
			e.addUser(testAdminID, "AdminUser", 1)
			m := e.fillQueueToMatch(users)
			e.expect(http.MethodPost, "/admin/match/"+m.ID+"/cancel"+tc.query, e.login(testAdminID), nil, http.StatusNoContent, "")
			queue, matches, _, _ := e.coord.GetState()
			if tc.wantQueue == 10 {
				if len(matches) != 1 {
					t.Errorf("requeued 10 players should restart acceptance, got %d matches", len(matches))
				}
				return
			}
			if len(queue) != tc.wantQueue || len(matches) != 0 {
				t.Errorf("queue=%d matches=%d", len(queue), len(matches))
			}
		})
	}
}

func TestAdminPlayerHandlers(t *testing.T) {
	e := newTestEnv(t)
	users := e.addPlayers(3)
	e.addUser(testAdminID, "AdminUser", 1)
	admin := e.login(testAdminID)
	ctx := context.Background()
	e.join(users[0])

	e.expect(http.MethodPost, "/admin/queue/kick/"+testSteamID(1), admin, nil, http.StatusNoContent, "")
	e.expect(http.MethodPost, "/admin/queue/kick/"+testSteamID(1), admin, nil, http.StatusBadRequest, "player not in queue")
	if ids := e.queueIDs(); len(ids) != 0 {
		t.Errorf("queue after kick = %v", ids)
	}

	e.expect(http.MethodPost, "/admin/player/"+testSteamID(2)+"/priority/7", admin, nil, http.StatusNoContent, "")
	e.expect(http.MethodPost, "/admin/player/"+testSteamID(2)+"/priority/11", admin, nil, http.StatusBadRequest, "priority must be 1-10")
	e.expect(http.MethodPost, "/admin/player/"+testSteamID(2)+"/priority/x", admin, nil, http.StatusBadRequest, "priority must be 1-10")
	if u, _ := e.store.GetUser(ctx, testSteamID(2)); u == nil || u.CaptainPriority != 7 {
		t.Errorf("priority not stored: %+v", u)
	}

	e.expect(http.MethodPost, "/admin/player/"+testSteamID(3)+"/ban", admin, url.Values{"duration": {"bogus"}}, http.StatusBadRequest, "duration must be")
	e.expect(http.MethodPost, "/admin/player/76561198000000777/ban", admin, url.Values{"duration": {"1d"}}, http.StatusBadRequest, "user not found")
	before := time.Now()
	e.expect(http.MethodPost, "/admin/player/"+testSteamID(3)+"/ban", admin, url.Values{"duration": {"7d"}}, http.StatusNoContent, "")
	u, _ := e.store.GetUser(ctx, testSteamID(3))
	if u == nil || u.BannedUntil == nil || u.BannedUntil.Before(before.Add(7*24*time.Hour-time.Minute)) || u.BannedUntil.After(time.Now().Add(7*24*time.Hour+time.Minute)) {
		t.Errorf("ban not stored for 7 days: %+v", u)
	}
	e.expect(http.MethodPost, "/queue/join", e.login(testSteamID(3)), nil, http.StatusForbidden, "You are banned until")

	e.expect(http.MethodPost, "/admin/player/"+testSteamID(3)+"/ban", admin, url.Values{"duration": {"indefinite"}}, http.StatusNoContent, "")
	if u, _ := e.store.GetUser(ctx, testSteamID(3)); u == nil || !u.IsBannedIndefinitely() {
		t.Errorf("indefinite ban not stored: %+v", u)
	}

	e.expect(http.MethodPost, "/admin/player/"+testSteamID(3)+"/unban", admin, nil, http.StatusNoContent, "")
	if u, _ := e.store.GetUser(ctx, testSteamID(3)); u == nil || u.BannedUntil != nil {
		t.Errorf("unban not stored: %+v", u)
	}
	e.expect(http.MethodPost, "/queue/join", e.login(testSteamID(3)), nil, http.StatusNoContent, "")
}

func TestAdminSettingsHandler(t *testing.T) {
	e := newTestEnv(t)
	e.addUser(testAdminID, "AdminUser", 1)
	admin := e.login(testAdminID)

	rec := e.request(http.MethodPost, "/admin/settings", admin, url.Values{"game_mode": {"cm"}, "league_id": {"12345"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/settings" {
		t.Errorf("status %d location %q", rec.Code, rec.Header().Get("Location"))
	}
	_, _, settings, _ := e.coord.GetState()
	if settings.GameMode != "cm" || settings.LeagueID != 12345 {
		t.Errorf("settings = %+v", settings)
	}
	e.expect(http.MethodPost, "/admin/settings", admin, url.Values{"game_mode": {"zz"}}, http.StatusBadRequest, "invalid game mode")
	e.expect(http.MethodPost, "/admin/settings", admin, url.Values{}, http.StatusBadRequest, "game_mode required")
	e.expect(http.MethodPost, "/admin/settings", admin, url.Values{"game_mode": {"ap"}, "league_id": {"abc"}}, http.StatusBadRequest, "league_id must be")
}

func TestAdminHistoryHandlers(t *testing.T) {
	e := newTestEnv(t)
	e.addPlayers(10)
	e.addUser(testAdminID, "AdminUser", 1)
	e.seedHistory()
	admin := e.login(testAdminID)
	ctx := context.Background()
	noResult := seededMatchIDs[1]
	broken := seededMatchIDs[2]

	e.expect(http.MethodPost, "/admin/history/"+noResult+"/result/dire", admin, nil, http.StatusNoContent, "")
	if m, _ := e.store.GetMatch(ctx, noResult); m == nil || m.Winner == nil || *m.Winner != "dire" {
		t.Errorf("history result not stored: %+v", m)
	}
	e.expect(http.MethodPost, "/admin/history/"+noResult+"/result/none", admin, nil, http.StatusNoContent, "")
	if m, _ := e.store.GetMatch(ctx, noResult); m == nil || m.Winner != nil {
		t.Errorf("history result not cleared: %+v", m)
	}
	e.expect(http.MethodPost, "/admin/history/"+noResult+"/result/bogus", admin, nil, http.StatusBadRequest, "winner must be")
	e.expect(http.MethodPost, "/admin/history/00000000-0000-4000-8000-000000000000/result/dire", admin, nil, http.StatusBadRequest, "match not found")

	e.expect(http.MethodPost, "/admin/history/"+broken+"/repair", admin, url.Values{"winner": {"radiant"}, "duration": {"-5"}}, http.StatusBadRequest, "duration must be")
	e.expect(http.MethodPost, "/admin/history/"+broken+"/repair", admin, url.Values{
		"winner":        {"radiant"},
		"ended_date":    {"2026-10-10"},
		"ended_time":    {"20:00"},
		"duration":      {"1800"},
		"dota_match_id": {"9000000033"},
	}, http.StatusNoContent, "")
	m, _ := e.store.GetMatch(ctx, broken)
	if m == nil || m.State != "completed" || m.Winner == nil || *m.Winner != "radiant" || m.Duration == nil || *m.Duration != 1800 || m.DotaMatchID != 9000000033 || m.EndedAt == nil {
		t.Errorf("repair not stored: %+v", m)
	}
}

func TestProfileUpdateHandler(t *testing.T) {
	e := newTestEnv(t)
	e.addPlayers(1)
	cookie := e.login(testSteamID(1))

	rec := e.request(http.MethodPost, "/profile", cookie, url.Values{"display_name": {"  Renamed  "}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile" {
		t.Errorf("status %d location %q", rec.Code, rec.Header().Get("Location"))
	}
	if u, _ := e.store.GetUser(context.Background(), testSteamID(1)); u == nil || u.Name != "Renamed" {
		t.Errorf("name not updated: %+v", u)
	}
	rec = e.request(http.MethodPost, "/profile", cookie, url.Values{"display_name": {""}})
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/profile?error=") {
		t.Errorf("empty name: status %d location %q", rec.Code, rec.Header().Get("Location"))
	}
}
