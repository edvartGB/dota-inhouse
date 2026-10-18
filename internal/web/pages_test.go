package web

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

type pageCase struct {
	name    string
	path    string
	as      string
	active  string
	markers []string
}

const brandName = "Den Norske Dota Ligaen"

var pageCases = []pageCase{
	{name: "index-anonymous", path: "/", active: "/", markers: []string{"Login with Steam"}},
	{name: "index-queued", path: "/", as: testSteamID(11), active: "/", markers: []string{"Player11", "Player12", "/queue/leave"}},
	{name: "index-accepting", path: "/", as: testSteamID(1), active: "/", markers: []string{`id="accept-btn"`}},
	{name: "index-admin", path: "/", as: testAdminID, active: "/", markers: []string{`href="/admin"`}},
	{name: "history", path: "/history", active: "/history", markers: []string{"9000000001", "Victory", "Page 1 of 1", "Player01"}},
	{name: "history-admin", path: "/history", as: testAdminID, active: "/history", markers: []string{"/admin/history/"}},
	{name: "leaderboard", path: "/leaderboard", active: "/leaderboard", markers: []string{"Player01", "Player06"}},
	{name: "profile", path: "/profile", as: testSteamID(1), active: "/profile", markers: []string{`name="display_name"`, "Player01"}},
	{name: "admin", path: "/admin", as: testAdminID, active: "/admin", markers: []string{"/admin/queue", "/admin/matches", "/admin/players", "/admin/settings", "/admin/bots", "/admin/broken-matches", "/admin/logs"}},
	{name: "admin-queue", path: "/admin/queue", as: testAdminID, active: "/admin", markers: []string{"Admin Queue", "Player11", "/admin/queue/kick/" + testSteamID(11)}},
	{name: "admin-matches", path: "/admin/matches", as: testAdminID, active: "/admin", markers: []string{"Admin Active Matches", "Accepting", "Cancel (Requeue)", "/result/radiant"}},
	{name: "admin-players", path: "/admin/players", as: testAdminID, active: "/admin", markers: []string{"Admin Players", "Player01", "banPlayer"}},
	{name: "admin-settings", path: "/admin/settings", as: testAdminID, active: "/admin", markers: []string{"Admin Lobby Settings", "Game Mode"}},
	{name: "admin-bots", path: "/admin/bots", as: testAdminID, active: "/admin", markers: []string{"Admin Bot Status", "No bots configured"}},
	{name: "admin-broken-matches", path: "/admin/broken-matches", as: testAdminID, active: "/admin", markers: []string{"Admin Broken Matches", "9000000003"}},
	{name: "admin-logs", path: "/admin/logs", as: testAdminID, active: "/admin", markers: []string{"Server Logs", "harness log line"}},
}

func newPopulatedEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newTestEnv(t)
	users := e.addPlayers(12)
	e.addUser(testAdminID, "AdminUser", 1)
	e.seedHistory()
	e.fillQueueToMatch(users)
	e.join(users[10])
	e.join(users[11])
	e.drainEvents()
	return e
}

func (e *testEnv) cookieFor(steamID string) *http.Cookie {
	if steamID == "" {
		return nil
	}
	return e.login(steamID)
}

func TestPagesRender(t *testing.T) {
	e := newPopulatedEnv(t)
	for _, pc := range pageCases {
		t.Run(pc.name, func(t *testing.T) {
			code, body := e.get(pc.path, e.cookieFor(pc.as))
			if code != http.StatusOK {
				t.Fatalf("GET %s: status %d, body: %s", pc.path, code, body)
			}
			for _, href := range []string{`href="/"`, `href="/history"`, `href="/leaderboard"`, `href="/profile"`} {
				if !strings.Contains(body, href) {
					t.Errorf("missing nav link %s", href)
				}
			}
			if !strings.Contains(body, "/static/styles.css") {
				t.Errorf("missing stylesheet link")
			}
			for _, m := range pc.markers {
				if !strings.Contains(body, m) {
					t.Errorf("missing %q", m)
				}
			}
		})
	}
}

var (
	titlePattern     = regexp.MustCompile(`<title>([^<]*)</title>`)
	stylesVersion    = regexp.MustCompile(`/static/styles\.css\?v=([0-9A-Za-z]+)`)
	appJSVersion     = regexp.MustCompile(`/static/app\.js\?v=([0-9A-Za-z]+)`)
	activeNavPattern = regexp.MustCompile(`<a href="([^"]*)" class="nav-link nav-link-active">`)
)

func TestAllPagesShareLayout(t *testing.T) {
	e := newPopulatedEnv(t)
	versions := map[string]map[string]bool{"styles": {}, "app": {}}
	for _, pc := range pageCases {
		t.Run(pc.name, func(t *testing.T) {
			_, body := e.get(pc.path, e.cookieFor(pc.as))
			if !strings.Contains(body, "<h1>"+brandName+"</h1>") {
				t.Errorf("header does not use brand name")
			}
			title := titlePattern.FindStringSubmatch(body)
			if title == nil || !strings.HasSuffix(title[1], brandName) {
				t.Errorf("title %v does not end with brand name", title)
			}
			if !strings.Contains(body, `<link rel="manifest" href="/manifest.json">`) {
				t.Errorf("missing manifest link")
			}
			if strings.Contains(body, "<style>") {
				t.Errorf("page has inline <style> block")
			}
			if m := stylesVersion.FindStringSubmatch(body); m == nil {
				t.Errorf("missing versioned styles.css")
			} else {
				versions["styles"][m[1]] = true
			}
			if m := appJSVersion.FindStringSubmatch(body); m == nil {
				t.Errorf("missing versioned app.js")
			} else {
				versions["app"][m[1]] = true
			}
			active := activeNavPattern.FindAllStringSubmatch(body, -1)
			if len(active) != 1 || active[0][1] != pc.active {
				t.Errorf("active nav links = %v, want only %s", active, pc.active)
			}
			if pc.as != "" {
				if !strings.Contains(body, `href="/auth/logout"`) {
					t.Errorf("logged-in page has no logout link")
				}
				if strings.Contains(body, "Login with Steam") {
					t.Errorf("logged-in page shows login button")
				}
			}
			if pc.as == testAdminID && !strings.Contains(body, `<a href="/admin" class="nav-link`) {
				t.Errorf("admin nav link missing for admin")
			}
		})
	}
	for kind, seen := range versions {
		if len(seen) > 1 {
			t.Errorf("%s asset versions differ between pages: %v", kind, seen)
		}
	}
}

func TestStaticAndServiceWorker(t *testing.T) {
	e := newTestEnv(t)
	for _, path := range []string{"/static/styles.css", "/static/app.js", "/static/vendor/htmx.min.js", "/static/vendor/idiomorph-ext.min.js", "/sw.js", "/manifest.json"} {
		if code, _ := e.get(path, nil); code != http.StatusOK {
			t.Errorf("GET %s: status %d", path, code)
		}
	}
}

func TestAuthGates(t *testing.T) {
	e := newPopulatedEnv(t)
	player := e.login(testSteamID(1))
	cases := []struct {
		method, path string
		cookie       *http.Cookie
		want         int
	}{
		{http.MethodGet, "/profile", nil, http.StatusUnauthorized},
		{http.MethodPost, "/queue/join", nil, http.StatusUnauthorized},
		{http.MethodGet, "/admin", nil, http.StatusUnauthorized},
		{http.MethodGet, "/admin", player, http.StatusForbidden},
		{http.MethodGet, "/admin/players", player, http.StatusForbidden},
		{http.MethodPost, "/admin/queue/closed", player, http.StatusForbidden},
		{http.MethodPost, "/admin/player/" + testSteamID(2) + "/ban", player, http.StatusForbidden},
	}
	for _, c := range cases {
		rec := e.request(c.method, c.path, c.cookie, nil)
		if rec.Code != c.want {
			t.Errorf("%s %s: status %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
}

func TestAdminStateJSON(t *testing.T) {
	e := newPopulatedEnv(t)
	code, body := e.get("/admin/state", e.login(testAdminID))
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	var state map[string]any
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !strings.Contains(body, testSteamID(11)) || !strings.Contains(body, testSteamID(1)) {
		t.Errorf("state JSON missing queue or match players: %s", body)
	}
}
