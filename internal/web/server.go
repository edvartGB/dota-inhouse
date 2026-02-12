package web

import (
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/edvart/dota-inhouse/internal/auth"
	"github.com/edvart/dota-inhouse/internal/coordinator"
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
}

type Config struct {
	DevMode       bool
	AdminSteamIDs string // Comma-separated list of admin Steam IDs
	PushService   *push.Service
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
	}

	s.setupRoutes(staticFS)
	return s
}

func (s *Server) setupRoutes(staticFS fs.FS) {
	r := s.router

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	r.Get("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(staticFS, "sw.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/javascript")
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
		r.Get("/admin/state", s.handleAdminState)
		r.Post("/admin/match/{matchID}/cancel", s.handleAdminCancelMatch)
		r.Post("/admin/match/{matchID}/result/{winner}", s.handleAdminSetResult)
		r.Post("/admin/queue/kick/{playerID}", s.handleAdminKickPlayer)
		r.Post("/admin/player/{playerID}/priority/{priority}", s.handleAdminSetCaptainPriority)
		r.Post("/admin/settings", s.handleAdminSetLobbySettings)
		r.Post("/admin/history/{matchID}/result/{winner}", s.handleAdminSetHistoryResult)
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

	queue, matches, _ := s.coordinator.GetState()

	matchList := make([]*coordinator.Match, 0, len(matches))
	for _, m := range matches {
		matchList = append(matchList, m)
	}

	data := PageData{
		User:    user,
		Queue:   queue,
		Matches: matchList,
		DevMode: s.devMode,
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
	User    interface{}
	Queue   []coordinator.Player
	Match   *coordinator.Match
	Matches []*coordinator.Match
	InQueue bool
	InMatch bool
	DevMode bool
}

type HistoryPageData struct {
	User    interface{}
	Matches []store.MatchWithPlayers
	DevMode bool
	IsAdmin bool
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	user, _ := s.sessions.GetUser(r.Context(), r)

	matches, err := s.store.ListMatchesWithPlayers(r.Context(), 50)
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
	DevMode    bool
}

func (s *Server) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	user, _ := s.sessions.GetUser(r.Context(), r)

	var startDate, endDate *time.Time
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

	preset := r.URL.Query().Get("preset")
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

	data := LeaderboardPageData{
		User:       user,
		Entries:    entries,
		StartDate:  startStr,
		EndDate:    endStr,
		FilterName: filterName,
		DevMode:    s.devMode,
	}

	if err := s.templates.ExecuteTemplate(w, "leaderboard.html", data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
