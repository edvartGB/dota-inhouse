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
)

// Manager manages a pool of Steam bots.
type Manager struct {
	bots          []*Bot
	commands      chan<- coordinator.Command
	mu            sync.Mutex
	matchToBotCtx map[string]context.CancelFunc
}

// Config holds bot configuration.
type Config struct {
	Bots []BotCredentials
}

// BotCredentials holds login credentials for a single bot.
type BotCredentials struct {
	Username string
	Password string
}

// NewManager creates a new bot manager with the given configuration.
func NewManager(cfg Config, commands chan<- coordinator.Command) *Manager {
	m := &Manager{
		bots:          make([]*Bot, 0, len(cfg.Bots)),
		commands:      commands,
		matchToBotCtx: make(map[string]context.CancelFunc),
	}

	for _, cred := range cfg.Bots {
		if cred.Username != "" && cred.Password != "" {
			bot := NewBot(cred.Username, cred.Password)
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

func (m *Manager) cancelMatch(matchID string) {
	m.mu.Lock()
	cancel, exists := m.matchToBotCtx[matchID]
	if exists {
		delete(m.matchToBotCtx, matchID)
	}
	m.mu.Unlock()

	if exists {
		log.Printf("Cancelling bot for match %s", matchID)
		cancel()
	}
}

func (m *Manager) handleLobbyRequest(ctx context.Context, req coordinator.RequestBotLobby) {
	log.Printf("Looking for available bot for match %s", req.MatchID)

	matchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	m.mu.Lock()
	m.matchToBotCtx[req.MatchID] = cancel
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.matchToBotCtx, req.MatchID)
		m.mu.Unlock()
	}()

	failedBots := make(map[*Bot]bool)

	for {
		select {
		case <-matchCtx.Done():
			log.Printf("Bot request cancelled for match %s", req.MatchID)
			return
		default:
		}

		bot := m.getAvailableBot(failedBots)
		if bot != nil {
			log.Printf("Assigning bot %s to match %s", bot.name, req.MatchID)
			if bot.CreateLobby(matchCtx, req.MatchID, req.Players, req.Radiant, req.Dire, req.GameMode, req.LeagueID, m.commands) {
				return
			}
			failedBots[bot] = true
			log.Printf("Bot %s failed to create lobby for match %s, trying another bot...", bot.name, req.MatchID)

			if len(failedBots) >= m.botCount() {
				log.Printf("All bots failed to create lobby for match %s, retrying in %v...", req.MatchID, BotRetryInterval)
				if !waitForRetry(matchCtx, BotRetryInterval) {
					log.Printf("Bot request cancelled for match %s", req.MatchID)
					return
				}
				failedBots = make(map[*Bot]bool)
			}
			continue
		}

		if len(failedBots) > 0 {
			log.Printf("No untried available bot for match %s, retrying in %v...", req.MatchID, BotRetryInterval)
		} else {
			log.Printf("No available bot for match %s, retrying in %v...", req.MatchID, BotRetryInterval)
		}

		if !waitForRetry(matchCtx, BotRetryInterval) {
			log.Printf("Bot request cancelled for match %s", req.MatchID)
			return
		}
	}
}

func waitForRetry(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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
