package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/edvart/dota-inhouse/internal/store"
)

func newAuthTestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "auth-test.db")
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func createUser(t *testing.T, st store.Store, steamID, name string) {
	t.Helper()
	now := time.Now()
	err := st.UpsertUser(context.Background(), &store.User{
		SteamID:         steamID,
		Name:            name,
		AvatarURL:       "",
		CaptainPriority: 5,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		t.Fatalf("failed to create user %s: %v", steamID, err)
	}
}

func createSessionCookie(t *testing.T, sm *SessionManager, steamID string) *http.Cookie {
	t.Helper()
	rr := httptest.NewRecorder()
	if err := sm.CreateSession(context.Background(), rr, steamID); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	resp := rr.Result()
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookieName {
			return c
		}
	}
	t.Fatal("session cookie not set")
	return nil
}

func TestSessionManagerCreateGetDelete(t *testing.T) {
	st := newAuthTestStore(t)
	sm := NewSessionManager(st)
	createUser(t, st, "u1", "Alice")

	// No cookie means no session.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	sess, err := sm.GetSession(context.Background(), req)
	if err != nil {
		t.Fatalf("get session without cookie failed: %v", err)
	}
	if sess != nil {
		t.Fatal("expected nil session without cookie")
	}

	cookie := createSessionCookie(t, sm, "u1")
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)

	sess, err = sm.GetSession(context.Background(), req)
	if err != nil {
		t.Fatalf("get session failed: %v", err)
	}
	if sess == nil || sess.SteamID != "u1" {
		t.Fatalf("unexpected session: %+v", sess)
	}

	user, err := sm.GetUser(context.Background(), req)
	if err != nil {
		t.Fatalf("get user from session failed: %v", err)
	}
	if user == nil || user.Name != "Alice" {
		t.Fatalf("unexpected user from session: %+v", user)
	}

	deleteRR := httptest.NewRecorder()
	if err := sm.DeleteSession(context.Background(), deleteRR, req); err != nil {
		t.Fatalf("delete session failed: %v", err)
	}

	// Old cookie should no longer resolve to an active session.
	reqAfterDelete := httptest.NewRequest(http.MethodGet, "/", nil)
	reqAfterDelete.AddCookie(cookie)
	sess, err = sm.GetSession(context.Background(), reqAfterDelete)
	if err != nil {
		t.Fatalf("get session after delete failed: %v", err)
	}
	if sess != nil {
		t.Fatal("expected nil session after delete")
	}
}

func TestRequireAuthMiddleware(t *testing.T) {
	st := newAuthTestStore(t)
	sm := NewSessionManager(st)
	createUser(t, st, "u1", "Alice")
	cookie := createSessionCookie(t, sm, "u1")

	mw := RequireAuth(sm)
	protected := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			t.Fatal("expected user in request context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	unauthReq := httptest.NewRequest(http.MethodGet, "/protected", nil)
	unauthRR := httptest.NewRecorder()
	protected.ServeHTTP(unauthRR, unauthReq)
	if unauthRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d", unauthRR.Code)
	}

	authReq := httptest.NewRequest(http.MethodGet, "/protected", nil)
	authReq.AddCookie(cookie)
	authRR := httptest.NewRecorder()
	protected.ServeHTTP(authRR, authReq)
	if authRR.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for authenticated request, got %d", authRR.Code)
	}
}

func TestAdminMiddleware(t *testing.T) {
	st := newAuthTestStore(t)
	sm := NewSessionManager(st)
	createUser(t, st, "admin1", "Admin")
	createUser(t, st, "user1", "Regular")

	adminCookie := createSessionCookie(t, sm, "admin1")
	userCookie := createSessionCookie(t, sm, "user1")

	cfg := NewAdminConfig("admin1")
	mw := AdminMiddleware(cfg, sm)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	unauthReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	unauthRR := httptest.NewRecorder()
	handler.ServeHTTP(unauthRR, unauthReq)
	if unauthRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d", unauthRR.Code)
	}

	nonAdminReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	nonAdminReq.AddCookie(userCookie)
	nonAdminRR := httptest.NewRecorder()
	handler.ServeHTTP(nonAdminRR, nonAdminReq)
	if nonAdminRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin request, got %d", nonAdminRR.Code)
	}

	adminReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	adminReq.AddCookie(adminCookie)
	adminRR := httptest.NewRecorder()
	handler.ServeHTTP(adminRR, adminReq)
	if adminRR.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for admin request, got %d", adminRR.Code)
	}
}

func TestNewAdminConfigParsing(t *testing.T) {
	cfg := NewAdminConfig(" id1, id2 , ,id3 ")
	if !cfg.IsAdmin("id1") || !cfg.IsAdmin("id2") || !cfg.IsAdmin("id3") {
		t.Fatalf("expected parsed admin IDs, got %+v", cfg.AdminSteamIDs)
	}
	if cfg.IsAdmin("unknown") {
		t.Fatal("did not expect unknown ID to be admin")
	}
}

func TestSteamIDFromOpenIDURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "https url",
			url:  "https://steamcommunity.com/openid/id/76561198000000000",
			want: "76561198000000000",
		},
		{
			name: "http url",
			url:  "http://steamcommunity.com/openid/id/76561198000000001",
			want: "76561198000000001",
		},
		{
			name: "invalid url",
			url:  "https://example.com/openid/id/76561198000000002",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SteamIDFromOpenIDURL(tt.url); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

