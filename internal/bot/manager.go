package bot

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/edvart/dota-inhouse/internal/coordinator"
)

const (
	// BotRetryInterval is how often to check for an available bot
	BotRetryInterval = 5 * time.Second
	// BotStartupInterval avoids authenticating every bot from one IP at once.
	BotStartupInterval = 15 * time.Second
)

// Manager manages a pool of Steam bots.
type lobbyTimerControl struct {
	Paused   bool
	Deadline time.Time
}

type Manager struct {
	bots                []*Bot
	commands            chan<- coordinator.Command
	baseURL             string
	mu                  sync.Mutex
	matchToBotCtx       map[string]context.CancelFunc
	matchToTimerControl map[string]chan lobbyTimerControl
}

// Config holds bot configuration.
type Config struct {
	Bots    []BotCredentials
	BaseURL string
	// SteamAPIKey enables the live match data probe. Optional.
	SteamAPIKey string
}

// BotCredentials holds login credentials for a single bot.
type BotCredentials struct {
	Username string
	Password string
}

// NewManager creates a new bot manager with the given configuration.
func NewManager(cfg Config, commands chan<- coordinator.Command) *Manager {
	m := &Manager{
		bots:                make([]*Bot, 0, len(cfg.Bots)),
		commands:            commands,
		baseURL:             cfg.BaseURL,
		matchToBotCtx:       make(map[string]context.CancelFunc),
		matchToTimerControl: make(map[string]chan lobbyTimerControl),
	}

	for _, cred := range cfg.Bots {
		if cred.Username != "" && cred.Password != "" {
			initialDelay := time.Duration(len(m.bots)) * BotStartupInterval
			bot := NewBot(cred.Username, cred.Password, initialDelay, cfg.SteamAPIKey)
			m.bots = append(m.bots, bot)
			log.Printf("Bot initialized: %s", cred.Username)
		}
	}

	if len(m.bots) == 0 {
		log.Println("Warning: No bots configured. Lobby creation will not work.")
	}

	return m
}

// Run listens for events and handles lobby requests.
func (m *Manager) Run(ctx context.Context, events <-chan coordinator.Event) {
	log.Println("Bot manager started")
	for {
		select {
		case <-ctx.Done():
			log.Println("Bot manager shutting down")
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			switch e := event.(type) {
			case coordinator.RequestBotLobby:
				go m.handleLobbyRequest(ctx, e)
			case coordinator.LobbyCountdownPaused:
				m.sendLobbyTimerControl(e.MatchID, lobbyTimerControl{Paused: true})
			case coordinator.LobbyCountdownResumed:
				m.sendLobbyTimerControl(e.MatchID, lobbyTimerControl{Deadline: e.Deadline})
			case coordinator.MatchAcceptStarted:
				go m.handleMatchAcceptStarted(e)
			case coordinator.MatchCancelled:
				m.cancelMatch(e.MatchID)
			case coordinator.MatchCancelledByAdmin:
				m.cancelMatch(e.MatchID)
			case coordinator.MatchCompleted:
				// Includes admin-forced results; once completed we should stop
				// tracking the lobby and free the bot immediately.
				m.cancelMatch(e.MatchID)
			}
		}
	}
}

func (m *Manager) handleMatchAcceptStarted(event coordinator.MatchAcceptStarted) {
	bot := m.getLoggedInBot()
	if bot == nil {
		log.Printf("No logged-in bot available for Steam accept notifications for match %s", event.MatchID)
		return
	}

	bot.NotifyMatchAccept(event.MatchID, event.Players, m.baseURL)
}

func (m *Manager) cancelMatch(matchID string) {
	m.mu.Lock()
	cancel, exists := m.matchToBotCtx[matchID]
	if exists {
		delete(m.matchToBotCtx, matchID)
	}
	delete(m.matchToTimerControl, matchID)
	m.mu.Unlock()

	if exists {
		log.Printf("Cancelling bot for match %s", matchID)
		cancel()
	}
}

func (m *Manager) sendLobbyTimerControl(matchID string, control lobbyTimerControl) {
	m.mu.Lock()
	controlCh := m.matchToTimerControl[matchID]
	m.mu.Unlock()
	if controlCh == nil {
		return
	}

	select {
	case controlCh <- control:
	default:
		log.Printf("Dropping lobby timer control for match %s: control channel full", matchID)
	}
}

func applyLobbyTimerControl(matchID string, control lobbyTimerControl, deadline *time.Time, paused *bool) {
	if control.Paused {
		*paused = true
		log.Printf("Lobby timer paused for match %s", matchID)
		return
	}

	*deadline = control.Deadline
	*paused = false
	log.Printf("Lobby timer resumed for match %s with deadline %s", matchID, control.Deadline.Format(time.RFC3339))
}

func waitForLobbyTimerResume(ctx context.Context, controlCh <-chan lobbyTimerControl, matchID string, deadline *time.Time, paused *bool) bool {
	for *paused {
		select {
		case <-ctx.Done():
			return false
		case control := <-controlCh:
			applyLobbyTimerControl(matchID, control, deadline, paused)
		}
	}
	return true
}

func waitForRetryOrControl(ctx context.Context, interval time.Duration, controlCh <-chan lobbyTimerControl, matchID string, deadline *time.Time, paused *bool) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case control := <-controlCh:
		applyLobbyTimerControl(matchID, control, deadline, paused)
		return true
	case <-timer.C:
		return true
	}
}

func (m *Manager) handleLobbyRequest(ctx context.Context, req coordinator.RequestBotLobby) {
	log.Printf("Looking for available bot for match %s", req.MatchID)

	matchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	controlCh := make(chan lobbyTimerControl, 4)

	m.mu.Lock()
	m.matchToBotCtx[req.MatchID] = cancel
	m.matchToTimerControl[req.MatchID] = controlCh
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.matchToBotCtx, req.MatchID)
		delete(m.matchToTimerControl, req.MatchID)
		m.mu.Unlock()
	}()

	lobbyDeadline := req.Deadline
	lobbyPaused := false
	failedBots := make(map[*Bot]bool)

	for {
		select {
		case <-matchCtx.Done():
			log.Printf("Bot request cancelled for match %s", req.MatchID)
			return
		case control := <-controlCh:
			applyLobbyTimerControl(req.MatchID, control, &lobbyDeadline, &lobbyPaused)
			continue
		default:
		}

		if lobbyPaused {
			if !waitForLobbyTimerResume(matchCtx, controlCh, req.MatchID, &lobbyDeadline, &lobbyPaused) {
				log.Printf("Bot request cancelled for match %s", req.MatchID)
				return
			}
			continue
		}

		if !lobbyDeadline.IsZero() && !time.Now().Before(lobbyDeadline) {
			log.Printf("Lobby deadline reached before bot assignment for match %s", req.MatchID)
			m.commands <- coordinator.BotLobbyTimeout{MatchID: req.MatchID, Deadline: lobbyDeadline}
			return
		}

		bot := m.getAvailableBot(failedBots)
		if bot != nil {
			log.Printf("Assigning bot %s to match %s", bot.name, req.MatchID)
			if bot.CreateLobby(matchCtx, req.MatchID, req.Players, req.Radiant, req.Dire, req.GameMode, req.LeagueID, lobbyDeadline, controlCh, m.commands) {
				return
			}
			failedBots[bot] = true
			log.Printf("Bot %s failed to create lobby for match %s, trying another bot...", bot.name, req.MatchID)

			if len(failedBots) >= m.botCount() {
				retryInterval := retryIntervalBeforeDeadline(lobbyDeadline)
				log.Printf("All bots failed to create lobby for match %s, retrying in %v...", req.MatchID, retryInterval)
				if !waitForRetryOrControl(matchCtx, retryInterval, controlCh, req.MatchID, &lobbyDeadline, &lobbyPaused) {
					log.Printf("Bot request cancelled for match %s", req.MatchID)
					return
				}
				failedBots = make(map[*Bot]bool)
			}
			continue
		}

		retryInterval := retryIntervalBeforeDeadline(lobbyDeadline)
		if len(failedBots) > 0 {
			log.Printf("No untried available bot for match %s, retrying in %v...", req.MatchID, retryInterval)
		} else {
			log.Printf("No available bot for match %s, retrying in %v...", req.MatchID, retryInterval)
		}

		if !waitForRetryOrControl(matchCtx, retryInterval, controlCh, req.MatchID, &lobbyDeadline, &lobbyPaused) {
			log.Printf("Bot request cancelled for match %s", req.MatchID)
			return
		}
	}
}

func retryIntervalBeforeDeadline(deadline time.Time) time.Duration {
	if deadline.IsZero() {
		return BotRetryInterval
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}
	if remaining < BotRetryInterval {
		return remaining
	}
	return BotRetryInterval
}

func (m *Manager) botCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.bots)
}

func (m *Manager) getAvailableBot(exclude map[*Bot]bool) *Bot {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, bot := range m.bots {
		if exclude[bot] {
			continue
		}
		if bot.IsAvailable() {
			return bot
		}
	}
	return nil
}

func (m *Manager) getLoggedInBot() *Bot {
	m.mu.Lock()
	bots := make([]*Bot, len(m.bots))
	copy(bots, m.bots)
	m.mu.Unlock()

	for _, bot := range bots {
		if bot.IsLoggedIn() {
			return bot
		}
	}
	return nil
}

// Statuses returns a snapshot of all configured bots.
func (m *Manager) Statuses() []Status {
	m.mu.Lock()
	bots := make([]*Bot, len(m.bots))
	copy(bots, m.bots)
	m.mu.Unlock()

	statuses := make([]Status, 0, len(bots))
	for _, bot := range bots {
		statuses = append(statuses, bot.Status())
	}

	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Name < statuses[j].Name
	})

	return statuses
}

// Shutdown disconnects all bots.
func (m *Manager) Shutdown() {
	log.Println("Shutting down all bots...")
	for _, bot := range m.bots {
		bot.Disconnect()
	}
}
