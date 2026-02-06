package bot

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/golang/protobuf/proto"
	"github.com/paralin/go-dota2"
	"github.com/paralin/go-dota2/cso"
	"github.com/paralin/go-dota2/protocol"
	"github.com/paralin/go-steam"
	"github.com/paralin/go-steam/steamid"
	"github.com/sirupsen/logrus"
)

// Bot represents a Steam/Dota 2 bot that can create and manage lobbies.
type Bot struct {
	name         string
	client       *steam.Client
	dota2Client  *dota2.Dota2
	loggedIn     bool
	busy         bool
	autoEndDelay time.Duration
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
}

// NewBot creates a new Steam bot with the given credentials.
func NewBot(username, password string) *Bot {
	bot := &Bot{
		name:   username,
		client: steam.NewClient(),
	}

	loginInfo := &steam.LogOnDetails{
		Username: username,
		Password: password,
	}

	go bot.connectWithRetry(loginInfo, 5, 10*time.Second)

	return bot
}

func (b *Bot) connectWithRetry(loginInfo *steam.LogOnDetails, maxRetries int, timeout time.Duration) {
	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Printf("[%s] Connection attempt %d/%d", b.name, attempt, maxRetries)

		firstEvent := b.attemptConnection(timeout)
		if firstEvent != nil {
			log.Printf("[%s] Connection established, listening to events", b.name)
			b.handleEvents(loginInfo, firstEvent)
			return
		}

		if attempt < maxRetries {
			backoff := time.Duration(attempt) * 5 * time.Second
			log.Printf("[%s] Connection failed, retrying in %v...", b.name, backoff)
			time.Sleep(backoff)

			b.mu.Lock()
			b.client = steam.NewClient()
			b.mu.Unlock()
		}
	}

	log.Printf("[%s] Failed to connect after %d attempts", b.name, maxRetries)
}

func (b *Bot) attemptConnection(timeout time.Duration) interface{} {
	eventChan := make(chan interface{}, 1)

	go func() {
		b.client.Connect()
		select {
		case event := <-b.client.Events():
			eventChan <- event
		case <-time.After(timeout):
			eventChan <- nil
		}
	}()

	select {
	case event := <-eventChan:
		if event != nil {
			return event
		}
		log.Printf("[%s] Connection timed out", b.name)
		b.client.Disconnect()
		return nil
	case <-time.After(timeout + 2*time.Second):
		log.Printf("[%s] Connection attempt timed out", b.name)
		b.client.Disconnect()
		return nil
	}
}

func (b *Bot) handleEvents(loginInfo *steam.LogOnDetails, firstEvent interface{}) {
	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.ctx = ctx
	b.cancel = cancel
	b.mu.Unlock()
	defer cancel()

	log.Printf("[%s] Listening to steam client events", b.name)

	b.processEvent(firstEvent, loginInfo)

	for event := range b.client.Events() {
		b.processEvent(event, loginInfo)
	}
}

func (b *Bot) processEvent(event interface{}, loginInfo *steam.LogOnDetails) {
	switch event.(type) {
	case *steam.ConnectedEvent:
		log.Printf("[%s] Connected, logging on…", b.name)
		b.client.Auth.LogOn(loginInfo)

	case *steam.LoggedOnEvent:
		b.mu.Lock()
		b.loggedIn = true
		b.mu.Unlock()
		log.Printf("[%s] Logged on successfully!", b.name)

	case *steam.DisconnectedEvent:
		log.Printf("[%s] Disconnected.", b.name)
		b.mu.Lock()
		b.loggedIn = false
		b.mu.Unlock()
	}
}

// IsAvailable returns true if the bot is logged in and not busy.
func (b *Bot) IsAvailable() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.loggedIn && !b.busy
}

// LobbyJoinTimeout is the duration players have to join the lobby.
const LobbyJoinTimeout = 1 * time.Minute

// CreateLobby creates a Dota 2 practice lobby for the match.
// Returns true if lobby was successfully created and monitored, false if it failed early.
func (b *Bot) CreateLobby(ctx context.Context, matchID string, players []coordinator.Player, radiant []coordinator.Player, dire []coordinator.Player, commands chan<- coordinator.Command) bool {
	b.mu.Lock()
	if !b.loggedIn {
		b.mu.Unlock()
		log.Printf("[%s] Cannot create lobby: not logged in", b.name)
		return false
	}
	b.busy = true
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.busy = false
		b.mu.Unlock()
	}()

	// Initialize Dota 2 client if needed
	if b.dota2Client == nil {
		log.Printf("[%s] Creating new Dota 2 client", b.name)
		logger := logrus.New()
		logger.SetLevel(logrus.WarnLevel)
		b.dota2Client = dota2.New(b.client, logger)
		b.dota2Client.SetPlaying(true)
		time.Sleep(time.Second)
		b.dota2Client.SayHello()
		time.Sleep(3 * time.Second)
	} else {
		log.Printf("[%s] Reusing existing Dota 2 client", b.name)
		b.dota2Client.SetPlaying(true)
		b.dota2Client.SayHello()
	}

	log.Printf("[%s] Creating lobby for match %s", b.name, matchID)

	lobbyName := fmt.Sprintf("Inhouse Match %s", matchID[:8])
	b.dota2Client.LeaveCreateLobby(b.ctx, &protocol.CMsgPracticeLobbySetDetails{
		AllowCheats: proto.Bool(true),
		GameName:    proto.String(lobbyName),
		GameMode:    proto.Uint32(uint32(protocol.DOTA_GameMode_DOTA_GAMEMODE_1V1MID)),
		Visibility:  protocol.DOTALobbyVisibility_DOTALobbyVisibility_Friends.Enum(),
	}, true)

	// Move bot to unassigned
	log.Printf("[%s] Moving bot to unassigned pool", b.name)
	b.dota2Client.JoinLobbyTeam(protocol.DOTA_GC_TEAM_DOTA_GC_TEAM_PLAYER_POOL, 1)
	time.Sleep(time.Second)

	// Invite all players
	log.Printf("[%s] Inviting players", b.name)
	for _, player := range players {
		id, err := strconv.ParseUint(player.SteamID, 10, 64)
		if err == nil {
			b.dota2Client.InviteLobbyMember(steamid.SteamId(id))
			log.Printf("[%s] Invited player: %s", b.name, player.Name)
		} else {
			log.Printf("[%s] Invalid steam ID for player %s: %v", b.name, player.Name, err)
		}
	}

	// Notify coordinator that lobby is ready
	commands <- coordinator.BotLobbyReady{MatchID: matchID}

	// Monitor lobby state with expected team assignments
	b.monitorLobbyState(ctx, matchID, radiant, dire, commands)
	return true
}

func (b *Bot) monitorLobbyState(ctx context.Context, matchID string, expectedRadiant []coordinator.Player, expectedDire []coordinator.Player, commands chan<- coordinator.Command) {
	eventCh, eventCancel, err := b.dota2Client.GetCache().SubscribeType(cso.Lobby)
	if err != nil {
		log.Printf("[%s] Failed to subscribe to lobby events: %v", b.name, err)
		// Clean up the lobby we created
		b.dota2Client.DestroyLobby(b.ctx)
		// Notify coordinator that lobby failed (no players joined)
		commands <- coordinator.BotLobbyTimeout{
			MatchID:            matchID,
			PlayersJoinedRight: []string{},
		}
		return
	}
	defer eventCancel()

	// Build expected team maps (Steam ID -> expected team)
	// Team 0 = Radiant (GOOD_GUYS), Team 1 = Dire (BAD_GUYS)
	expectedTeam := make(map[uint64]int)
	for _, p := range expectedRadiant {
		if id, err := strconv.ParseUint(p.SteamID, 10, 64); err == nil {
			expectedTeam[id] = 0
		}
	}
	for _, p := range expectedDire {
		if id, err := strconv.ParseUint(p.SteamID, 10, 64); err == nil {
			expectedTeam[id] = 1
		}
	}

	var lastState protocol.CSODOTALobby_State = protocol.CSODOTALobby_UI
	var currentLobby *protocol.CSODOTALobby // Track latest lobby state
	launched := false
	gameEnded := false
	var endGameOnce sync.Once

	// Start lobby join timeout
	timeoutTimer := time.NewTimer(LobbyJoinTimeout)
	defer timeoutTimer.Stop()

	log.Printf("[%s] Started monitoring lobby state (timeout: %v)", b.name, LobbyJoinTimeout)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s] Lobby monitoring cancelled", b.name)
			return

		case <-timeoutTimer.C:
			if !launched && !gameEnded {
				log.Printf("[%s] Lobby join timeout reached", b.name)
				// Get list of players who joined correctly from last known state
				joinedCorrectly := b.getCorrectlyJoinedPlayers(currentLobby, expectedTeam)
				commands <- coordinator.BotLobbyTimeout{
					MatchID:            matchID,
					PlayersJoinedRight: joinedCorrectly,
				}
				b.dota2Client.DestroyLobby(b.ctx)
				return
			}

		case lobbyEvent, ok := <-eventCh:
			if !ok {
				log.Printf("[%s] Lobby event channel closed", b.name)
				return
			}

			dota2Lobby := lobbyEvent.Object.(*protocol.CSODOTALobby)
			currentLobby = dota2Lobby // Update tracked lobby state
			currentState := dota2Lobby.GetState()

			if currentState != lastState {
				log.Printf("[%s] Lobby state changed: %v -> %v", b.name, lastState, currentState)
				lastState = currentState

				switch currentState {
				case protocol.CSODOTALobby_UI:
					log.Printf("[%s] Lobby in UI state (setup)", b.name)

				case protocol.CSODOTALobby_READYUP:
					log.Printf("[%s] Ready check phase", b.name)

				case protocol.CSODOTALobby_SERVERSETUP:
					log.Printf("[%s] Server is being set up", b.name)

				case protocol.CSODOTALobby_RUN:
					log.Printf("[%s] *** GAME IS NOW RUNNING ***", b.name)
					commands <- coordinator.BotGameStarted{
						MatchID:     matchID,
						DotaMatchID: dota2Lobby.GetMatchId(),
					}

				case protocol.CSODOTALobby_POSTGAME:
					log.Printf("[%s] *** GAME HAS ENDED ***", b.name)
					dotaMatchID := dota2Lobby.GetMatchId()
					endGameOnce.Do(func() {
						gameEnded = true
						commands <- coordinator.BotGameEnded{
							MatchID:     matchID,
							DotaMatchID: dotaMatchID,
						}
						b.dota2Client.DestroyLobby(b.ctx)
					})
					return

				case protocol.CSODOTALobby_NOTREADY:
					log.Printf("[%s] Lobby not ready", b.name)
				}
			}

			// Check if all players are on correct teams and launch
			if currentState == protocol.CSODOTALobby_UI && !launched && !gameEnded {
				if b.checkAllPlayersCorrect(dota2Lobby, expectedTeam) {
					log.Printf("[%s] All players on correct teams! Starting game...", b.name)
					launched = true
					timeoutTimer.Stop() // Cancel timeout since we're launching
					b.dota2Client.LaunchLobby()
					log.Printf("[%s] Game launch command sent!", b.name)
				}
			}
		}
	}
}

// checkAllPlayersCorrect verifies all expected players are on their correct teams.
func (b *Bot) checkAllPlayersCorrect(dota2Lobby *protocol.CSODOTALobby, expectedTeam map[uint64]int) bool {
	if dota2Lobby == nil {
		return false
	}

	// Track which expected players have joined correctly
	correctCount := 0
	expectedCount := len(expectedTeam)

	for _, member := range dota2Lobby.AllMembers {
		steamID := member.GetId()
		actualTeam := member.GetTeam()

		expected, isExpected := expectedTeam[steamID]
		if !isExpected {
			continue // Not an expected player (could be spectator or bot)
		}

		// Check if player is on correct team
		var onCorrectTeam bool
		if expected == 0 && actualTeam == protocol.DOTA_GC_TEAM_DOTA_GC_TEAM_GOOD_GUYS {
			onCorrectTeam = true
		} else if expected == 1 && actualTeam == protocol.DOTA_GC_TEAM_DOTA_GC_TEAM_BAD_GUYS {
			onCorrectTeam = true
		}

		if onCorrectTeam {
			correctCount++
		}
	}

	log.Printf("[%s] Players on correct teams: %d/%d", b.name, correctCount, expectedCount)
	return correctCount == expectedCount
}

// getCorrectlyJoinedPlayers returns Steam IDs of players who joined on their correct team.
func (b *Bot) getCorrectlyJoinedPlayers(dota2Lobby *protocol.CSODOTALobby, expectedTeam map[uint64]int) []string {
	var correct []string
	if dota2Lobby == nil {
		return correct
	}

	for _, member := range dota2Lobby.AllMembers {
		steamID := member.GetId()
		actualTeam := member.GetTeam()

		expected, isExpected := expectedTeam[steamID]
		if !isExpected {
			continue
		}

		var onCorrectTeam bool
		if expected == 0 && actualTeam == protocol.DOTA_GC_TEAM_DOTA_GC_TEAM_GOOD_GUYS {
			onCorrectTeam = true
		} else if expected == 1 && actualTeam == protocol.DOTA_GC_TEAM_DOTA_GC_TEAM_BAD_GUYS {
			onCorrectTeam = true
		}

		if onCorrectTeam {
			correct = append(correct, strconv.FormatUint(steamID, 10))
		}
	}

	return correct
}

// Disconnect cleanly disconnects the bot from Steam.
func (b *Bot) Disconnect() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.dota2Client != nil {
		b.dota2Client.SetPlaying(false)
		b.dota2Client.Close()
	}

	if b.client != nil && b.loggedIn {
		log.Printf("[%s] Disconnecting from Steam...", b.name)
		b.client.Disconnect()
		b.loggedIn = false
	}
}
