package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/edvart/dota-inhouse/internal/auth"
	"github.com/edvart/dota-inhouse/internal/bot"
	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/edvart/dota-inhouse/internal/dotaapi"
	"github.com/edvart/dota-inhouse/internal/matchrecorder"
	"github.com/edvart/dota-inhouse/internal/store"
	"github.com/edvart/dota-inhouse/internal/web"
)

func main() {
	// Configuration from environment
	port := getEnv("PORT", "8080")
	baseURL := getEnv("BASE_URL", "http://localhost:"+port)
	steamAPIKey := getEnv("STEAM_API_KEY", "")
	dbPath := getEnv("DATABASE_PATH", "./data/inhouse.db")
	devMode := getEnv("DEV_MODE", "") == "true"

	// Bot credentials (host bots)
	bot1User := getEnv("BOT1_USERNAME", "")
	bot1Pass := getEnv("BOT1_PASSWORD", "")

	bot2User := getEnv("BOT2_USERNAME", "")
	bot2Pass := getEnv("BOT2_PASSWORD", "")

	bot3User := getEnv("BOT3_USERNAME", "")
	bot3Pass := getEnv("BOT3_PASSWORD", "")

	// Admin Steam IDs (comma-separated)
	adminSteamIDs := getEnv("ADMIN_STEAM_IDS", "")

	// Configurable max players
	if maxPlayersStr := getEnv("MAX_PLAYERS", ""); maxPlayersStr != "" {
		if n, err := strconv.Atoi(maxPlayersStr); err == nil && n >= 2 {
			coordinator.MaxPlayers = n
			log.Printf("MaxPlayers set to %d", n)
		} else {
			log.Printf("Warning: invalid MAX_PLAYERS %q (must be integer >= 2)", maxPlayersStr)
		}
	}

	// Find project root (where web/ directory is)
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		log.Fatal("Could not find project root (looking for web/ directory)")
	}

	if steamAPIKey == "" && !devMode {
		log.Println("Warning: STEAM_API_KEY not set. Steam login will not work.")
	}

	// Ensure data directory exists
	if err := os.MkdirAll("./data", 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// Initialize store
	db, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Initialize coordinator
	coord := coordinator.New()

	// Initialize auth
	sessions := auth.NewSessionManager(db)
	steamAuth := auth.NewSteamAuth(steamAPIKey, baseURL, db, sessions)

	// Create fake users in dev mode
	if devMode {
		log.Println("Dev mode enabled")
		if err := steamAuth.CreateFakeUsers(context.Background(), 15); err != nil {
			log.Printf("Failed to create fake users: %v", err)
		}
	}

	// Load templates from filesystem
	templatesDir := filepath.Join(projectRoot, "web", "templates")
	templates, err := web.LoadTemplatesFromDir(templatesDir)
	if err != nil {
		log.Fatalf("Failed to load templates: %v", err)
	}

	// Get static files directory
	staticDir := filepath.Join(projectRoot, "web", "static")
	staticFS := os.DirFS(staticDir)

	// Initialize web server
	server := web.NewServer(coord, steamAuth, sessions, db, templates, staticFS, web.Config{
		DevMode:       devMode,
		AdminSteamIDs: adminSteamIDs,
	})

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start coordinator
	go coord.Run(ctx)

	// Start SSE hub
	server.StartSSE(coord.Events())

	// Initialize Dota API client (uses same Steam API key)
	var dotaAPIClient *dotaapi.Client
	if steamAPIKey != "" {
		dotaAPIClient = dotaapi.NewClient(steamAPIKey)
		log.Println("Dota API client initialized")
	} else {
		log.Println("Warning: No Steam API key, match details won't be fetched from Dota API")
	}

	// Start match recorder
	recorder := matchrecorder.New(db, dotaAPIClient)
	recorderEvents := coord.Subscribe()
	go recorder.Run(ctx, recorderEvents)

	// Start session cleanup job (runs every hour)
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := db.DeleteExpiredSessions(ctx); err != nil {
					log.Printf("Failed to cleanup expired sessions: %v", err)
				} else {
					log.Println("Cleaned up expired sessions")
				}
			}
		}
	}()

	// Initialize and start bot manager if credentials are configured
	var botManager *bot.Manager
	botCreds := []bot.BotCredentials{
		{Username: bot1User, Password: bot1Pass},
		{Username: bot2User, Password: bot2Pass},
		{Username: bot3User, Password: bot3Pass},
	}
	// Filter out empty credentials
	var validCreds []bot.BotCredentials
	for _, cred := range botCreds {
		if cred.Username != "" && cred.Password != "" {
			validCreds = append(validCreds, cred)
		}
	}
	if len(validCreds) > 0 {
		// Create a command channel for bots to send commands back
		botCommands := make(chan coordinator.Command, 100)
		go func() {
			for cmd := range botCommands {
				coord.Send(cmd)
			}
		}()

		botManager = bot.NewManager(bot.Config{
			Bots: validCreds,
		}, botCommands)
		botEvents := coord.Subscribe()
		go botManager.Run(ctx, botEvents)
	} else {
		log.Println("No bot credentials configured. Lobby creation will be skipped.")
	}

	// Start HTTP server
	httpServer := &http.Server{
		Addr:    ":" + port,
		Handler: server,
	}

	// Handle shutdown signals
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop

		log.Println("Shutting down...")
		cancel()

		// Shutdown bots
		if botManager != nil {
			botManager.Shutdown()
		}

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
		}
	}()

	fmt.Printf("Server running on http://localhost:%s\n", port)
	if devMode {
		fmt.Printf("Dev login: http://localhost:%s/dev/login?steamid=test1&name=TestUser\n", port)
	}

	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}

	log.Println("Server stopped")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func findProjectRoot() string {
	// Start from current directory and walk up looking for web/ directory
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		webDir := filepath.Join(dir, "web")
		if info, err := os.Stat(webDir); err == nil && info.IsDir() {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root
			return ""
		}
		dir = parent
	}
}
