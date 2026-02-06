package web

import (
	"html/template"
	"io/fs"
	"log"
	"net/http"

	"github.com/edvart/dota-inhouse/internal/auth"
	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server holds the HTTP server and its dependencies.
type Server struct {
	router      *chi.Mux
	coordinator *coordinator.Coordinator
	steamAuth   *auth.SteamAuth
	sessions    *auth.SessionManager
	sse         *SSEHub
	templates   *template.Template
	devMode     bool
}

// Config holds server configuration.
type Config struct {
	DevMode bool
}

// NewServer creates a new HTTP server.
func NewServer(
	coord *coordinator.Coordinator,
	steamAuth *auth.SteamAuth,
	sessions *auth.SessionManager,
	templates *template.Template,
	staticFS fs.FS,
	cfg Config,
) *Server {
	s := &Server{
		router:      chi.NewRouter(),
		coordinator: coord,
		steamAuth:   steamAuth,
		sessions:    sessions,
		sse:         NewSSEHub(templates, coord, cfg.DevMode),
		templates:   templates,
		devMode:     cfg.DevMode,
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

	queue, matches := s.coordinator.GetState()

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
