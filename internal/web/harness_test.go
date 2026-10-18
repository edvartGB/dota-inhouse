package web

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/edvart/dota-inhouse/internal/auth"
	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/edvart/dota-inhouse/internal/store"
)

func TestMain(m *testing.M) {
	if os.Getenv("TEST_LOGS") == "" {
		log.SetOutput(io.Discard)
		middleware.DefaultLogger = middleware.RequestLogger(&middleware.DefaultLogFormatter{Logger: log.New(io.Discard, "", 0)})
	}
	os.Exit(m.Run())
}

const testAdminID = "76561198000000099"

var seededMatchIDs = []string{
	"11111111-aaaa-4aaa-8aaa-111111111111",
	"22222222-bbbb-4bbb-8bbb-222222222222",
	"33333333-cccc-4ccc-8ccc-333333333333",
}

type testEnv struct {
	t       *testing.T
	store   *store.SQLiteStore
	coord   *coordinator.Coordinator
	srv     *Server
	events  <-chan coordinator.Event
	cookies map[string]*http.Cookie
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()

	st, err := store.NewSQLiteStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	coord := coordinator.New()
	events := coord.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	go coord.Run(ctx)
	t.Cleanup(cancel)

	tmpl, err := LoadTemplatesFromDir("../../web/templates")
	if err != nil {
		t.Fatalf("loading templates: %v", err)
	}

	logPath := filepath.Join(dir, "inhouse.log")
	if err := os.WriteFile(logPath, []byte("2026/10/18 20:00:00 harness log line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sessions := auth.NewSessionManager(st)
	steamAuth := auth.NewSteamAuth("", "http://localhost", st, sessions)
	srv := NewServer(coord, steamAuth, sessions, st, tmpl, os.DirFS("../../web/static"), Config{
		AdminSteamIDs: testAdminID,
		LogPath:       logPath,
	})

	return &testEnv{t: t, store: st, coord: coord, srv: srv, events: events, cookies: map[string]*http.Cookie{}}
}

func testSteamID(i int) string {
	return fmt.Sprintf("765611980000000%02d", i)
}

func testPlayerName(i int) string {
	return fmt.Sprintf("Player%02d", i)
}

func (e *testEnv) addUser(steamID, name string, priority int) store.User {
	e.t.Helper()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	u := store.User{SteamID: steamID, Name: name, CaptainPriority: priority, CreatedAt: now, UpdatedAt: now}
	if err := e.store.UpsertUser(context.Background(), &u); err != nil {
		e.t.Fatalf("adding user: %v", err)
	}
	return u
}

func (e *testEnv) addPlayers(n int) []store.User {
	e.t.Helper()
	users := make([]store.User, 0, n)
	for i := 1; i <= n; i++ {
		priority := 11 - i
		if priority < 1 {
			priority = 1
		}
		users = append(users, e.addUser(testSteamID(i), testPlayerName(i), priority))
	}
	return users
}

func (e *testEnv) login(steamID string) *http.Cookie {
	e.t.Helper()
	if c, ok := e.cookies[steamID]; ok {
		return c
	}
	sessionID := "session-" + steamID
	err := e.store.CreateSession(context.Background(), &store.Session{
		ID:        sessionID,
		SteamID:   steamID,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		e.t.Fatalf("creating session: %v", err)
	}
	c := &http.Cookie{Name: auth.SessionCookieName, Value: sessionID}
	e.cookies[steamID] = c
	return c
}

func (e *testEnv) request(method, path string, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) get(path string, cookie *http.Cookie) (int, string) {
	e.t.Helper()
	rec := e.request(http.MethodGet, path, cookie, nil)
	return rec.Code, rec.Body.String()
}

func (e *testEnv) join(u store.User) {
	e.t.Helper()
	resp := make(chan error, 1)
	e.coord.Send(coordinator.JoinQueue{
		Player: coordinator.Player{
			SteamID:         u.SteamID,
			Name:            u.Name,
			AvatarURL:       u.AvatarURL,
			CaptainPriority: u.CaptainPriority,
		},
		Response: resp,
	})
	if err := <-resp; err != nil {
		e.t.Fatalf("join %s: %v", u.Name, err)
	}
}

func (e *testEnv) sendOK(build func(resp chan error) coordinator.Command) {
	e.t.Helper()
	resp := make(chan error, 1)
	e.coord.Send(build(resp))
	if err := <-resp; err != nil {
		e.t.Fatalf("command failed: %v", err)
	}
}

func (e *testEnv) sync() {
	e.coord.GetState()
}

func (e *testEnv) drainEvents() []coordinator.Event {
	e.sync()
	var out []coordinator.Event
	for {
		select {
		case ev := <-e.events:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func (e *testEnv) currentMatch() *coordinator.Match {
	e.t.Helper()
	_, matches, _, _ := e.coord.GetState()
	if len(matches) != 1 {
		e.t.Fatalf("expected exactly one active match, got %d", len(matches))
	}
	for _, m := range matches {
		return m
	}
	return nil
}

func (e *testEnv) acceptAll(m *coordinator.Match) {
	e.t.Helper()
	for _, p := range m.Players {
		id := p.SteamID
		e.sendOK(func(r chan error) coordinator.Command {
			return coordinator.AcceptMatch{PlayerID: id, MatchID: m.ID, Response: r}
		})
	}
}

func (e *testEnv) chooseSide(m *coordinator.Match, side string) {
	e.t.Helper()
	chooser := m.Captains[m.SideChooserIndex].SteamID
	e.sendOK(func(r chan error) coordinator.Command {
		return coordinator.ChooseSide{CaptainID: chooser, MatchID: m.ID, Side: side, Response: r}
	})
}

func (e *testEnv) pickOnce() {
	e.t.Helper()
	m := e.currentMatch()
	captain := m.Captains[m.CurrentPicker].SteamID
	picked := m.AvailablePlayers[0].SteamID
	e.sendOK(func(r chan error) coordinator.Command {
		return coordinator.PickPlayer{CaptainID: captain, PickedID: picked, MatchID: m.ID, Response: r}
	})
}

func (e *testEnv) draftToCompletion() {
	e.t.Helper()
	for {
		m := e.currentMatch()
		if m.State != coordinator.MatchStateDrafting {
			return
		}
		e.pickOnce()
	}
}

func (e *testEnv) fillQueueToMatch(users []store.User) *coordinator.Match {
	e.t.Helper()
	for _, u := range users[:coordinator.MaxPlayers] {
		e.join(u)
	}
	return e.currentMatch()
}

func (e *testEnv) matchWaitingForBot(users []store.User) *coordinator.Match {
	e.t.Helper()
	m := e.fillQueueToMatch(users)
	e.acceptAll(m)
	e.chooseSide(e.currentMatch(), "radiant")
	e.draftToCompletion()
	return e.currentMatch()
}

func (e *testEnv) seedHistory() {
	e.t.Helper()
	ctx := context.Background()
	radiant := "radiant"
	duration := 2400
	start := time.Date(2026, 10, 10, 19, 0, 0, 0, time.UTC)
	end := start.Add(40 * time.Minute)

	seed := func(id string, dotaID uint64, state string, winner *string, dur *int, ended *time.Time) {
		match := &store.Match{ID: id, DotaMatchID: dotaID, State: state, StartedAt: start, EndedAt: ended, Winner: winner, Duration: dur}
		var players []store.MatchPlayer
		for i := 1; i <= 10; i++ {
			team := "radiant"
			if i > 5 {
				team = "dire"
			}
			players = append(players, store.MatchPlayer{
				MatchID:    id,
				SteamID:    testSteamID(i),
				Team:       team,
				WasCaptain: i == 1 || i == 6,
				Accepted:   true,
			})
		}
		if err := e.store.CreateMatchWithPlayers(ctx, match, players); err != nil {
			e.t.Fatalf("seeding match %s: %v", id, err)
		}
	}

	seed(seededMatchIDs[0], 9000000001, "completed", &radiant, &duration, &end)
	seed(seededMatchIDs[1], 9000000002, "completed", nil, nil, &end)
	seed(seededMatchIDs[2], 9000000003, "in_progress", nil, nil, nil)
}
