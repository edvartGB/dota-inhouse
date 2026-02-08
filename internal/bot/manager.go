package bot

import (
	"context"
	"log"
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
	bots         []*Bot
	commands     chan<- coordinator.Command
	mu           sync.Mutex
	matchToBotCtx map[string]context.CancelFunc
}

// Config holds bot configuration.
type Config struct {
	Bots         []BotCredentials
	AutoEndDelay time.Duration
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
	m.mu.Lock()
	m.matchToBotCtx[req.MatchID] = cancel
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.matchToBotCtx, req.MatchID)
		m.mu.Unlock()
	}()

	for {
		bot := m.getAvailableBot()
		if bot != nil {
			log.Printf("Assigning bot %s to match %s", bot.name, req.MatchID)
			if bot.CreateLobby(matchCtx, req.MatchID, req.Players, req.Radiant, req.Dire, req.GameMode, m.commands) {
				return
			}
			log.Printf("Bot %s failed to create lobby, trying another...", bot.name)
			continue
		}

		log.Printf("No available bot for match %s, retrying in %v...", req.MatchID, BotRetryInterval)

		select {
		case <-matchCtx.Done():
			log.Printf("Bot request cancelled for match %s", req.MatchID)
			return
		case <-time.After(BotRetryInterval):
		}
	}
}

func (m *Manager) getAvailableBot() *Bot {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, bot := range m.bots {
		if bot.IsAvailable() {
			return bot
		}
	}
	return nil
}

// Shutdown disconnects all bots.
func (m *Manager) Shutdown() {
	log.Println("Shutting down all bots...")
	for _, bot := range m.bots {
		bot.Disconnect()
	}
}
