package web

import (
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/edvart/dota-inhouse/internal/auth"
	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/edvart/dota-inhouse/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server holds the HTTP server and its dependencies.
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
}

// Config holds server configuration.
type Config struct {
	DevMode       bool
	AdminSteamIDs string // Comma-separated list of admin Steam IDs
}

// NewServer creates a new HTTP server.
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
	}

	s.setupRoutes(staticFS)
	return s
}

func (s *Server) setupRoutes(staticFS fs.FS) {
	r := s.router

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)

	// Static files
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Auth routes
	r.Get("/auth/login", s.steamAuth.LoginHandler)
	r.Get("/auth/callback", s.steamAuth.CallbackHandler)
	r.Get("/auth/logout", s.steamAuth.LogoutHandler)
	r.Get("/me", s.steamAuth.MeHandler)

	// Dev mode routes
	if s.devMode {
		r.Get("/dev/login", s.steamAuth.DevLoginHandler)
		r.Post("/dev/add-fake-players", s.handleAddFakePlayers)
		r.Post("/dev/accept-all", s.handleDevAcceptAll)
		r.Post("/dev/pick/{matchID}/{playerID}", s.handleDevPick)
		// Bot simulation endpoints
		r.Post("/dev/bot/game-started/{matchID}", s.handleDevBotGameStarted)
		r.Post("/dev/bot/game-ended/{matchID}", s.handleDevBotGameEnded)
		r.Post("/dev/bot/lobby-timeout/{matchID}", s.handleDevBotLobbyTimeout)
	}

	// SSE endpoint
	r.Get("/events", s.handleSSE)

	// Queue routes (require auth)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth(s.sessions))

		r.Post("/queue/join", s.handleJoinQueue)
		r.Post("/queue/leave", s.handleLeaveQueue)
		r.Post("/match/{matchID}/accept", s.handleAcceptMatch)
		r.Post("/match/{matchID}/pick/{playerID}", s.handlePickPlayer)
	})

	// Main page
	r.Get("/", s.handleIndex)

	// Match history
	r.Get("/history", s.handleHistory)

	// Leaderboard
	r.Get("/leaderboard", s.handleLeaderboard)

	// Admin routes (require admin)
	r.Group(func(r chi.Router) {
		r.Use(auth.AdminMiddleware(s.adminConfig, s.sessions))

		r.Get("/admin", s.handleAdminPage)
		r.Get("/admin/state", s.handleAdminState)
		r.Post("/admin/match/{matchID}/cancel", s.handleAdminCancelMatch)
		r.Post("/admin/match/{matchID}/result/{winner}", s.handleAdminSetResult)
		r.Post("/admin/queue/kick/{playerID}", s.handleAdminKickPlayer)
		r.Post("/admin/player/{playerID}/priority/{priority}", s.handleAdminSetCaptainPriority)
		r.Post("/admin/settings", s.handleAdminSetLobbySettings)
	})
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// StartSSE starts the SSE hub goroutine.
func (s *Server) StartSSE(events <-chan coordinator.Event) {
	go s.sse.Run(events)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	user, _ := s.sessions.GetUser(r.Context(), r)

	queue, matches, _ := s.coordinator.GetState()

	// Convert matches map to slice for template
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

	// Check if user is in queue or match
	if user != nil {
		for _, p := range queue {
			if p.SteamID == user.SteamID {
				data.InQueue = true
				break
			}
		}
		// Get the user's specific match (if any)
		data.Match = s.coordinator.GetPlayerMatch(user.SteamID)
		data.InMatch = data.Match != nil
	}

	if err := s.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// PageData holds data for the main page template.
type PageData struct {
	User    interface{}
	Queue   []coordinator.Player
	Match   *coordinator.Match
	Matches []*coordinator.Match
	InQueue bool
	InMatch bool
	DevMode bool
}

// HistoryPageData holds data for the history page template.
type HistoryPageData struct {
	User    interface{}
	Matches []store.MatchWithPlayers
	DevMode bool
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	user, _ := s.sessions.GetUser(r.Context(), r)

	matches, err := s.store.ListMatchesWithPlayers(r.Context(), 50)
	if err != nil {
		log.Printf("Failed to load match history: %v", err)
		http.Error(w, "Failed to load history", http.StatusInternalServerError)
		return
	}

	data := HistoryPageData{
		User:    user,
		Matches: matches,
		DevMode: s.devMode,
	}

	if err := s.templates.ExecuteTemplate(w, "history.html", data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// LeaderboardPageData holds data for the leaderboard page template.
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

	// Parse date filters
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
			// Set to end of day
			endOfDay := t.Add(24*time.Hour - time.Second)
			endDate = &endOfDay
		}
	}

	// Handle preset filters
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
