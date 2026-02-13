package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/edvart/dota-inhouse/internal/push"
	"github.com/edvart/dota-inhouse/internal/store"
	"github.com/edvart/dota-inhouse/internal/web"
)

func main() {
	// Log to both stdout and a file for searchability
	logPath := getEnv("LOG_PATH", "./data/inhouse.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		log.Fatalf("Failed to create log directory: %v", err)
	}
	rotateLogFile(logPath, 10*1024*1024) // rotate at 10MB
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	defer logFile.Close()
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.SetOutput(io.MultiWriter(os.Stdout, logFile))

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

	// Web Push VAPID keys
	vapidPublicKey := getEnv("VAPID_PUBLIC_KEY", "")
	vapidPrivateKey := getEnv("VAPID_PRIVATE_KEY", "")
	vapidSubject := getEnv("VAPID_SUBJECT", "mailto:noreply@example.com")

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

	// Restore queue from disk and set up persistence
	queuePath := filepath.Join(filepath.Dir(dbPath), "queue.json")
	if savedQueue := loadQueue(queuePath, db); len(savedQueue) > 0 {
		coord.RestoreQueue(savedQueue)
		log.Printf("Restored %d players to queue from %s", len(savedQueue), queuePath)
	}
	coord.SetQueuePersistence(func(queue []coordinator.Player) {
		if err := saveQueue(queuePath, queue); err != nil {
			log.Printf("Failed to save queue: %v", err)
		}
	})

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

	// Initialize push notification service
	var pushService *push.Service
	if vapidPublicKey != "" && vapidPrivateKey != "" {
		pushService = push.NewService(db, push.Config{
			VAPIDPublicKey:  vapidPublicKey,
			VAPIDPrivateKey: vapidPrivateKey,
			VAPIDSubject:    vapidSubject,
		})
		log.Println("Web Push notifications enabled")
	} else {
		log.Println("Warning: VAPID keys not set. Web Push notifications will not work.")
		log.Println("Run 'go run cmd/generate-vapid/main.go' to generate keys")
	}

	// Initialize web server
	server := web.NewServer(coord, steamAuth, sessions, db, templates, staticFS, web.Config{
		DevMode:       devMode,
		AdminSteamIDs: adminSteamIDs,
		PushService:   pushService,
		LogPath:       logPath,
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

	// Start push notifier if push service is enabled
	if pushService != nil {
		pushNotifier := push.NewNotifier(pushService)
		pushEvents := coord.Subscribe()
		go pushNotifier.Run(ctx, pushEvents)
		log.Println("Push notifier started")
	}

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

// rotateLogFile renames the log file to .old if it exceeds maxBytes.
// Keeps one backup only. Errors are non-fatal (logged to stderr).
func rotateLogFile(path string, maxBytes int64) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxBytes {
		return
	}
	oldPath := path + ".old"
	os.Remove(oldPath)
	if err := os.Rename(path, oldPath); err != nil {
		fmt.Fprintf(os.Stderr, "Log rotation failed: %v\n", err)
	}
}

// loadQueue reads the saved queue from a JSON file and refreshes player data from the DB.
func loadQueue(path string, db store.Store) []coordinator.Player {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var players []coordinator.Player
	if err := json.Unmarshal(data, &players); err != nil {
		log.Printf("Failed to parse queue file: %v", err)
		return nil
	}
	// Refresh player data from DB (name/avatar/priority may have changed)
	var result []coordinator.Player
	for _, p := range players {
		user, err := db.GetUser(context.Background(), p.SteamID)
		if err != nil || user == nil {
			continue // User no longer exists
		}
		result = append(result, coordinator.Player{
			SteamID:         user.SteamID,
			Name:            user.Name,
			AvatarURL:       user.AvatarURL,
			CaptainPriority: user.CaptainPriority,
		})
	}
	return result
}

func saveQueue(path string, queue []coordinator.Player) error {
	data, err := json.Marshal(queue)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
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
