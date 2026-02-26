package web

import (
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/edvart/dota-inhouse/internal/auth"
	"github.com/edvart/dota-inhouse/internal/bot"
	"github.com/edvart/dota-inhouse/internal/coordinator"
	discordsvc "github.com/edvart/dota-inhouse/internal/discord"
	"github.com/edvart/dota-inhouse/internal/push"
	"github.com/edvart/dota-inhouse/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	router      *chi.Mux
	coordinator *coordinator.Coordinator
	steamAuth   *auth.SteamAuth
	sessions    *auth.SessionManager
	store       store.Store
	sse         *SSEHub
	templates   *template.Template
	devMode     bool
	adminConfig *auth.AdminConfig
	pushService *push.Service
	discordSvc  *discordsvc.Service
	botManager  *bot.Manager
	logPath     string
}

type Config struct {
	DevMode        bool
	AdminSteamIDs  string // Comma-separated list of admin Steam IDs
	PushService    *push.Service
	DiscordService *discordsvc.Service
	BotManager     *bot.Manager
	LogPath        string
}

func NewServer(
	coord *coordinator.Coordinator,
	steamAuth *auth.SteamAuth,
	sessions *auth.SessionManager,
	st store.Store,
	templates *template.Template,
	staticFS fs.FS,
	cfg Config,
) *Server {
	s := &Server{
		router:      chi.NewRouter(),
		coordinator: coord,
		steamAuth:   steamAuth,
		sessions:    sessions,
		store:       st,
		sse:         NewSSEHub(templates, coord, cfg.DevMode),
		templates:   templates,
		devMode:     cfg.DevMode,
		adminConfig: auth.NewAdminConfig(cfg.AdminSteamIDs),
		pushService: cfg.PushService,
		discordSvc:  cfg.DiscordService,
		botManager:  cfg.BotManager,
		logPath:     cfg.LogPath,
	}

	s.setupRoutes(staticFS)
	return s
}

func (s *Server) setupRoutes(staticFS fs.FS) {
	r := s.router

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)

	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))
	r.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Disable browser caching for JS/CSS during active development.
		if strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".css") {
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		}
		staticHandler.ServeHTTP(w, r)
	}))

	r.Get("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(staticFS, "sw.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Write(data)
	})

	r.Get("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(staticFS, "manifest.json")
		if err != nil {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/manifest+json")
		w.Write(data)
	})

	r.Get("/auth/login", s.steamAuth.LoginHandler)
	r.Get("/auth/callback", s.steamAuth.CallbackHandler)
	r.Get("/auth/logout", s.steamAuth.LogoutHandler)
	r.Get("/me", s.steamAuth.MeHandler)

	if s.devMode {
		r.Get("/dev/login", s.steamAuth.DevLoginHandler)
		r.Post("/dev/add-fake-players", s.handleAddFakePlayers)
		r.Post("/dev/accept-all", s.handleDevAcceptAll)
		r.Post("/dev/pick/{matchID}/{playerID}", s.handleDevPick)
		r.Post("/dev/bot/game-started/{matchID}", s.handleDevBotGameStarted)
		r.Post("/dev/bot/game-ended/{matchID}", s.handleDevBotGameEnded)
		r.Post("/dev/bot/lobby-timeout/{matchID}", s.handleDevBotLobbyTimeout)
	}

	r.Get("/events", s.handleSSE)

	// Push notification endpoints
	r.Get("/api/push/vapid-public-key", s.handleGetVAPIDPublicKey)

	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth(s.sessions))

		r.Get("/profile", s.handleProfilePage)
		r.Post("/profile", s.handleProfileUpdate)
		r.Post("/queue/join", s.handleJoinQueue)
		r.Post("/queue/leave", s.handleLeaveQueue)
		r.Post("/match/{matchID}/accept", s.handleAcceptMatch)
		r.Post("/match/{matchID}/pick/{playerID}", s.handlePickPlayer)

		// Push subscription management
		r.Post("/api/push/subscribe", s.handleSubscribePush)
		r.Post("/api/push/unsubscribe", s.handleUnsubscribePush)
		r.Post("/api/push/test", s.handleTestPush)
	})

	r.Get("/", s.handleIndex)
	r.Get("/history", s.handleHistory)
	r.Get("/leaderboard", s.handleLeaderboard)

	r.Group(func(r chi.Router) {
		r.Use(auth.AdminMiddleware(s.adminConfig, s.sessions))

		r.Get("/admin", s.handleAdminPage)
		r.Get("/admin/queue", s.handleAdminQueuePage)
		r.Get("/admin/matches", s.handleAdminMatchesPage)
		r.Get("/admin/captains", s.handleAdminCaptainsPage)
		r.Get("/admin/settings", s.handleAdminSettingsPage)
		r.Get("/admin/discord", s.handleAdminDiscordPage)
		r.Get("/admin/bots", s.handleAdminBotsPage)
		r.Get("/admin/broken-matches", s.handleAdminBrokenMatchesPage)
		r.Get("/admin/state", s.handleAdminState)
		r.Post("/admin/match/{matchID}/cancel", s.handleAdminCancelMatch)
		r.Post("/admin/match/{matchID}/result/{winner}", s.handleAdminSetResult)
		r.Post("/admin/queue/kick/{playerID}", s.handleAdminKickPlayer)
		r.Post("/admin/player/{playerID}/priority/{priority}", s.handleAdminSetCaptainPriority)
		r.Post("/admin/settings", s.handleAdminSetLobbySettings)
		r.Post("/admin/discord/ping", s.handleAdminDiscordPing)
		r.Post("/admin/queue/{status}", s.handleAdminSetQueueStatus)
		r.Post("/admin/history/{matchID}/result/{winner}", s.handleAdminSetHistoryResult)
		r.Post("/admin/history/{matchID}/repair", s.handleAdminRepairHistoryMatch)
		r.Get("/admin/logs", s.handleAdminLogs)
	})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) StartSSE(events <-chan coordinator.Event) {
	go s.sse.Run(events)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	user, _ := s.sessions.GetUser(r.Context(), r)

	queue, matches, _, queueOpen := s.coordinator.GetState()

	matchList := make([]*coordinator.Match, 0, len(matches))
	for _, m := range matches {
		matchList = append(matchList, m)
	}

	data := PageData{
		User:      user,
		Queue:     queue,
		Matches:   matchList,
		QueueOpen: queueOpen,
		DevMode:   s.devMode,
	}

	if user != nil {
		for _, p := range queue {
			if p.SteamID == user.SteamID {
				data.InQueue = true
				break
			}
		}
		data.Match = s.coordinator.GetPlayerMatch(user.SteamID)
		data.InMatch = data.Match != nil
	}

	if err := s.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

type PageData struct {
	User      interface{}
	Queue     []coordinator.Player
	Match     *coordinator.Match
	Matches   []*coordinator.Match
	InQueue   bool
	InMatch   bool
	QueueOpen bool
	DevMode   bool
}

type HistoryPageData struct {
	User         interface{}
	Matches      []store.MatchWithPlayers
	DevMode      bool
	IsAdmin      bool
	Page         int
	TotalPages   int
	TotalMatches int
	HasPrev      bool
	HasNext      bool
	PrevPage     int
	NextPage     int
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	user, _ := s.sessions.GetUser(r.Context(), r)

	const pageSize = 20
	page := 1
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if parsedPage, err := strconv.Atoi(pageParam); err == nil && parsedPage > 0 {
			page = parsedPage
		}
	}
	offset := (page - 1) * pageSize

	totalMatches, err := s.store.CountCompletedMatches(r.Context())
	if err != nil {
		log.Printf("Failed to count match history: %v", err)
		http.Error(w, "Failed to load history", http.StatusInternalServerError)
		return
	}

	totalPages := (totalMatches + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
		offset = (page - 1) * pageSize
	}

	matches, err := s.store.ListMatchesWithPlayersPage(r.Context(), pageSize, offset)
	if err != nil {
		log.Printf("Failed to load match history: %v", err)
		http.Error(w, "Failed to load history", http.StatusInternalServerError)
		return
	}

	isAdmin := false
	if user != nil {
		isAdmin = s.adminConfig.IsAdmin(user.SteamID)
	}

	data := HistoryPageData{
		User:    user,
		Matches: matches,
		DevMode: s.devMode,
		IsAdmin: isAdmin,
		Page:    page,
		TotalPages: totalPages,
		TotalMatches: totalMatches,
		HasPrev: page > 1,
		HasNext: page < totalPages,
		PrevPage: page - 1,
		NextPage: page + 1,
	}

	if err := s.templates.ExecuteTemplate(w, "history.html", data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

type LeaderboardPageData struct {
	User       interface{}
	Entries    []store.LeaderboardEntry
	StartDate  string
	EndDate    string
	FilterName string
	Preset     string
	SortBy     string
	SortDir    string
	DevMode    bool
}

func sanitizeLeaderboardSort(sortBy, sortDir string) (string, string) {
	sortBy = strings.ToLower(sortBy)
	sortDir = strings.ToLower(sortDir)

	switch sortBy {
	case "name", "wins", "losses", "delta", "winrate", "streak":
	default:
		sortBy = "delta"
	}

	if sortDir != "asc" && sortDir != "desc" {
		sortDir = "desc"
	}

	return sortBy, sortDir
}

func compareLeaderboardField(a, b store.LeaderboardEntry, sortBy string) int {
	switch sortBy {
	case "name":
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	case "wins":
		return compareInt(a.Wins, b.Wins)
	case "losses":
		return compareInt(a.Losses, b.Losses)
	case "winrate":
		return compareFloat(a.WinRate, b.WinRate)
	case "streak":
		return compareInt(a.Streak, b.Streak)
	default:
		return compareInt(a.Delta, b.Delta)
	}
}

func compareLeaderboardTieBreak(a, b store.LeaderboardEntry) int {
	if cmp := compareInt(b.Delta, a.Delta); cmp != 0 {
		return cmp
	}
	if cmp := compareInt(b.Total, a.Total); cmp != 0 {
		return cmp
	}
	return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func sortLeaderboardEntries(entries []store.LeaderboardEntry, sortBy, sortDir string) {
	direction := 1
	if sortDir == "desc" {
		direction = -1
	}

	sort.SliceStable(entries, func(i, j int) bool {
		a := entries[i]
		b := entries[j]

		cmp := compareLeaderboardField(a, b, sortBy)
		if cmp == 0 {
			cmp = compareLeaderboardTieBreak(a, b)
		}

		return cmp*direction < 0
	})
}

func (s *Server) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	user, _ := s.sessions.GetUser(r.Context(), r)

	var startDate, endDate *time.Time
	preset := r.URL.Query().Get("preset")
	sortBy, sortDir := sanitizeLeaderboardSort(r.URL.Query().Get("sort"), r.URL.Query().Get("dir"))
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")
	filterName := "All Time"

	if startStr != "" {
		if t, err := time.Parse("2006-01-02", startStr); err == nil {
			startDate = &t
		}
	}
	if endStr != "" {
		if t, err := time.Parse("2006-01-02", endStr); err == nil {
			endOfDay := t.Add(24*time.Hour - time.Second)
			endDate = &endOfDay
		}
	}

	now := time.Now()
	switch preset {
	case "week":
		start := now.AddDate(0, 0, -7)
		startDate = &start
		filterName = "Last 7 Days"
	case "month":
		start := now.AddDate(0, -1, 0)
		startDate = &start
		filterName = "Last 30 Days"
	case "year":
		start := now.AddDate(-1, 0, 0)
		startDate = &start
		filterName = "Last Year"
	default:
		if startStr != "" || endStr != "" {
			filterName = "Custom Range"
		}
	}

	entries, err := s.store.GetLeaderboard(r.Context(), startDate, endDate)
	if err != nil {
		log.Printf("Failed to load leaderboard: %v", err)
		http.Error(w, "Failed to load leaderboard", http.StatusInternalServerError)
		return
	}
	sortLeaderboardEntries(entries, sortBy, sortDir)

	data := LeaderboardPageData{
		User:       user,
		Entries:    entries,
		StartDate:  startStr,
		EndDate:    endStr,
		FilterName: filterName,
		Preset:     preset,
		SortBy:     sortBy,
		SortDir:    sortDir,
		DevMode:    s.devMode,
	}

	if r.Header.Get("HX-Request") == "true" {
		if err := s.templates.ExecuteTemplate(w, "leaderboard-table", data); err != nil {
			log.Printf("Template error: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	if err := s.templates.ExecuteTemplate(w, "leaderboard.html", data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
