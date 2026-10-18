package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/edvart/dota-inhouse/internal/store"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

func testSubscriptionKeys(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(auth)
}

type pushTestEnv struct {
	service     *Service
	store       *store.SQLiteStore
	server      *httptest.Server
	connections atomic.Int32
	requests    atomic.Int32
	mu          sync.Mutex
	paths       []string
}

func (env *pushTestEnv) waitForPaths(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && int(env.requests.Load()) < n {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	env.mu.Lock()
	defer env.mu.Unlock()
	got := slices.Clone(env.paths)
	slices.Sort(got)
	env.paths = nil
	env.requests.Store(0)
	return got
}

func TestNotifierSendsToMatchPlayersAndCaptains(t *testing.T) {
	env := newPushTestEnv(t, func(string) int { return http.StatusCreated })
	env.subscribeUser(t, "u1", "/u1")
	env.subscribeUser(t, "u2", "/u2")
	env.subscribeUser(t, "u3", "/u3")
	n := NewNotifier(env.service)
	ctx := context.Background()

	n.handleEvent(ctx, coordinator.MatchAcceptStarted{MatchID: "m1", Players: []coordinator.Player{{SteamID: "u1"}, {SteamID: "u2"}}})
	if got := env.waitForPaths(t, 2); !slices.Equal(got, []string{"/u1", "/u2"}) {
		t.Errorf("match found pushes = %v", got)
	}

	n.handleEvent(ctx, coordinator.DraftStarted{MatchID: "m1", Captains: [2]coordinator.Player{{SteamID: "u3"}, {SteamID: "u1"}}})
	if got := env.waitForPaths(t, 2); !slices.Equal(got, []string{"/u1", "/u3"}) {
		t.Errorf("draft pushes = %v", got)
	}

	n.handleEvent(ctx, coordinator.MatchCancelled{MatchID: "m1", FailedPlayers: []coordinator.Player{{SteamID: "u2"}}})
	n.handleEvent(ctx, coordinator.QueueUpdated{})
	if got := env.waitForPaths(t, 0); len(got) != 0 {
		t.Errorf("unexpected pushes = %v", got)
	}
}

func newPushTestEnv(t *testing.T, status func(path string) int) *pushTestEnv {
	t.Helper()
	env := &pushTestEnv{}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env.requests.Add(1)
		env.mu.Lock()
		env.paths = append(env.paths, r.URL.Path)
		env.mu.Unlock()
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(status(r.URL.Path))
		w.Write([]byte("push service response body"))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			env.connections.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	env.server = server

	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env.store = st

	now := time.Now()
	for _, id := range []string{"u1", "u2", "u3"} {
		if err := st.UpsertUser(context.Background(), &store.User{SteamID: id, Name: id, CaptainPriority: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}

	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	env.service = NewService(st, Config{VAPIDPublicKey: public, VAPIDPrivateKey: private, VAPIDSubject: "mailto:test@example.com"})
	return env
}

func (env *pushTestEnv) subscribe(t *testing.T, paths ...string) {
	t.Helper()
	env.subscribeUser(t, "u1", paths...)
}

func (env *pushTestEnv) subscribeUser(t *testing.T, steamID string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		p256dh, auth := testSubscriptionKeys(t)
		err := env.store.SavePushSubscription(context.Background(), &store.PushSubscription{
			SteamID:  steamID,
			Endpoint: env.server.URL + p,
			P256dh:   p256dh,
			Auth:     auth,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSendToUserReusesConnectionAcrossSubscriptions(t *testing.T) {
	env := newPushTestEnv(t, func(string) int { return http.StatusCreated })
	env.subscribe(t, "/a", "/b", "/c")
	http.DefaultTransport.(*http.Transport).CloseIdleConnections()

	if err := env.service.SendToUser(context.Background(), "u1", NotificationPayload{Title: "t", Body: "b"}); err != nil {
		t.Fatalf("SendToUser: %v", err)
	}
	if got := env.requests.Load(); got != 3 {
		t.Fatalf("push requests = %d, want 3", got)
	}
	if got := env.connections.Load(); got != 1 {
		t.Errorf("opened %d connections for 3 subscriptions, want 1 (response bodies must be released per request)", got)
	}
}

func TestSendToUserRemovesGoneSubscriptions(t *testing.T) {
	env := newPushTestEnv(t, func(path string) int {
		if path == "/gone" {
			return http.StatusGone
		}
		return http.StatusCreated
	})
	env.subscribe(t, "/ok", "/gone")

	if err := env.service.SendToUser(context.Background(), "u1", NotificationPayload{Title: "t"}); err != nil {
		t.Fatalf("SendToUser: %v", err)
	}
	subs, err := env.store.GetPushSubscriptions(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].Endpoint != env.server.URL+"/ok" {
		t.Errorf("remaining subscriptions = %+v", subs)
	}
}

func TestSendToUserFailsWhenAllSubscriptionsFail(t *testing.T) {
	env := newPushTestEnv(t, func(string) int { return http.StatusInternalServerError })
	env.subscribe(t, "/a")
	if err := env.service.SendToUser(context.Background(), "u1", NotificationPayload{Title: "t"}); err == nil {
		t.Error("expected error when every push fails")
	}
}
