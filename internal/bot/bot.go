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

type Status struct {
	Name          string
	LoggedIn      bool
	Busy          bool
	HasDotaClient bool
	State         string
}

func NewBot(username, password string) *Bot {
	bot := &Bot{
		name:   username,
		client: steam.NewClient(),
	}

	loginInfo := &steam.LogOnDetails{
		Username: username,
		Password: password,
	}

	go bot.connectWithRetry(loginInfo, 10*time.Second)

	return bot
}

func (b *Bot) connectWithRetry(loginInfo *steam.LogOnDetails, timeout time.Duration) {
	attempt := 0
	for {
		attempt++
		log.Printf("[%s] Connection attempt %d", b.name, attempt)

		firstConnected := b.attemptConnection(timeout)
		if firstConnected != nil {
			log.Printf("[%s] Connection established, listening to events", b.name)
			b.handleEvents(loginInfo, firstConnected)
			// If handleEvents returns, the connection was lost - reconnect
			log.Printf("[%s] Connection lost, will reconnect...", b.name)
			attempt = 0 // Reset attempt counter after successful connection
		}

		// Calculate backoff: 5s, 10s, 15s, ... up to 60s max
		backoff := time.Duration(attempt) * 5 * time.Second
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}
		log.Printf("[%s] Connection failed, retrying in %v...", b.name, backoff)
		time.Sleep(backoff)

		b.mu.Lock()
		b.client = steam.NewClient()
		b.loggedIn = false
		b.dota2Client = nil
		b.mu.Unlock()
	}
}

func (b *Bot) attemptConnection(timeout time.Duration) *steam.ConnectedEvent {
	go b.client.Connect()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			log.Printf("[%s] Connection attempt timed out", b.name)
			b.client.Disconnect()
			return nil

		case event, ok := <-b.client.Events():
			if !ok {
				log.Printf("[%s] Steam event stream closed during connect", b.name)
				return nil
			}

			switch e := event.(type) {
			case *steam.ConnectedEvent:
				return e
			case *steam.DisconnectedEvent:
				log.Printf("[%s] Disconnected before connect completed", b.name)
				return nil
			default:
				log.Printf("[%s] Ignoring pre-connect event %T while waiting for ConnectedEvent", b.name, event)
			}
		}
	}
}

func (b *Bot) handleEvents(loginInfo *steam.LogOnDetails, firstEvent interface{}) {
	const logonTimeout = 20 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.ctx = ctx
	b.cancel = cancel
	b.mu.Unlock()
	defer cancel()

	log.Printf("[%s] Listening to steam client events", b.name)

	if b.processEvent(firstEvent, loginInfo) {
		return
	}

	logonTimer := time.NewTimer(logonTimeout)
	defer logonTimer.Stop()
	waitingForLogon := true

	for {
		select {
		case <-logonTimer.C:
			b.mu.Lock()
			isLoggedIn := b.loggedIn
			b.mu.Unlock()

			if waitingForLogon && !isLoggedIn {
				log.Printf("[%s] Logon timed out after %v, forcing reconnect", b.name, logonTimeout)
				b.client.Disconnect()
				return
			}
			waitingForLogon = false

		case event, ok := <-b.client.Events():
			if !ok {
				return
			}

			if b.processEvent(event, loginInfo) {
				return
			}
			if waitingForLogon {
				if _, ok := event.(*steam.LoggedOnEvent); ok {
					waitingForLogon = false
					if !logonTimer.Stop() {
						select {
						case <-logonTimer.C:
						default:
						}
					}
				}
			}
		}
	}
}

func (b *Bot) processEvent(event interface{}, loginInfo *steam.LogOnDetails) bool {
	switch event.(type) {
	case *steam.ConnectedEvent:
		log.Printf("[%s] Connected, logging on…", b.name)
		b.client.Auth.LogOn(loginInfo)
		return false

	case *steam.LoggedOnEvent:
		b.mu.Lock()
		b.loggedIn = true
		b.mu.Unlock()
		log.Printf("[%s] Logged on successfully!", b.name)
		return false

	case *steam.DisconnectedEvent:
		log.Printf("[%s] Disconnected.", b.name)
		b.mu.Lock()
		b.loggedIn = false
		dotaClient := b.dota2Client
		b.dota2Client = nil
		b.mu.Unlock()
		if dotaClient != nil {
			dotaClient.SetPlaying(false)
			dotaClient.Close()
		}
		// Exit the event loop so connectWithRetry can establish a fresh connection.
		return true
	}

	return false
}

func (b *Bot) IsAvailable() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.loggedIn && !b.busy
}

func (b *Bot) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()

	status := Status{
		Name:          b.name,
		LoggedIn:      b.loggedIn,
		Busy:          b.busy,
		HasDotaClient: b.dota2Client != nil,
	}

	switch {
	case !status.LoggedIn:
		status.State = "disconnected"
	case status.Busy:
		status.State = "busy"
	default:
		status.State = "available"
	}

	return status
}

const (
	LobbyJoinTimeout         = 5 * time.Minute
	LobbyRelaunchDelay       = 3 * time.Second
	MaxLobbyRelaunchAttempts = 2
)

func gameModeFromString(mode string) protocol.DOTA_GameMode {
	switch mode {
	case "ap":
		return protocol.DOTA_GameMode_DOTA_GAMEMODE_AP
	case "cm":
		return protocol.DOTA_GameMode_DOTA_GAMEMODE_CM
	case "cd":
		return protocol.DOTA_GameMode_DOTA_GAMEMODE_CD
	case "rd":
		return protocol.DOTA_GameMode_DOTA_GAMEMODE_RD
	case "ar":
		return protocol.DOTA_GameMode_DOTA_GAMEMODE_AR
	default:
		return protocol.DOTA_GameMode_DOTA_GAMEMODE_CM
	}
}

func (b *Bot) CreateLobby(ctx context.Context, matchID string, players []coordinator.Player, radiant []coordinator.Player, dire []coordinator.Player, gameMode string, leagueID uint32, commands chan<- coordinator.Command) bool {
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
	dotaGameMode := gameModeFromString(gameMode)
	log.Printf("[%s] Creating lobby with game mode: %s (%v), league_id: %d", b.name, gameMode, dotaGameMode, leagueID)
	lobbyDetails := &protocol.CMsgPracticeLobbySetDetails{
		AllowCheats:     proto.Bool(false),
		AllowSpectating: proto.Bool(true),
		GameName:        proto.String(lobbyName),
		GameMode:        proto.Uint32(uint32(dotaGameMode)),
		Visibility:      protocol.DOTALobbyVisibility_DOTALobbyVisibility_Public.Enum(),
		DotaTvDelay:     protocol.LobbyDotaTVDelay_LobbyDotaTV_10.Enum(),
	}
	if leagueID > 0 {
		lobbyDetails.Leagueid = proto.Uint32(leagueID)
	}
	b.dota2Client.LeaveCreateLobby(b.ctx, lobbyDetails, true)

	log.Printf("[%s] Moving bot to unassigned pool", b.name)
	b.dota2Client.JoinLobbyTeam(protocol.DOTA_GC_TEAM_DOTA_GC_TEAM_PLAYER_POOL, 1)
	time.Sleep(time.Second)

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

	commands <- coordinator.BotLobbyReady{MatchID: matchID}

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
	relaunchAttempts := 0
	var endGameOnce sync.Once
	var relaunchTimer *time.Timer
	var relaunchTimerCh <-chan time.Time

	// Start lobby join timeout
	timeoutTimer := time.NewTimer(LobbyJoinTimeout)
	defer timeoutTimer.Stop()
	defer func() {
		if relaunchTimer != nil {
			relaunchTimer.Stop()
		}
	}()

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
				prevState := lastState
				log.Printf("[%s] Lobby state changed: %v -> %v", b.name, prevState, currentState)
				lastState = currentState

				switch currentState {
				case protocol.CSODOTALobby_UI:
					log.Printf("[%s] Lobby in UI state (setup)", b.name)
					// Some lobbies regress RUN -> UI when players fail to connect.
					// Schedule a delayed relaunch if this happens.
					if prevState == protocol.CSODOTALobby_RUN && !gameEnded {
						launched = false
						if relaunchAttempts < MaxLobbyRelaunchAttempts {
							relaunchAttempts++
							if relaunchTimer != nil {
								relaunchTimer.Stop()
							}
							relaunchTimer = time.NewTimer(LobbyRelaunchDelay)
							relaunchTimerCh = relaunchTimer.C
							log.Printf("[%s] Lobby regressed RUN -> UI, scheduling relaunch attempt %d/%d in %v",
								b.name, relaunchAttempts, MaxLobbyRelaunchAttempts, LobbyRelaunchDelay)
						} else {
							log.Printf("[%s] Lobby regressed RUN -> UI but max relaunch attempts reached (%d)",
								b.name, MaxLobbyRelaunchAttempts)
						}
					}

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
					if !timeoutTimer.Stop() {
						select {
						case <-timeoutTimer.C:
						default:
						}
					}
					b.dota2Client.LaunchLobby()
					log.Printf("[%s] Game launch command sent!", b.name)
				}
			}

		case <-relaunchTimerCh:
			relaunchTimerCh = nil
			if gameEnded || launched || currentLobby == nil {
				continue
			}
			if currentLobby.GetState() != protocol.CSODOTALobby_UI {
				continue
			}

			if b.checkAllPlayersCorrect(currentLobby, expectedTeam) {
				log.Printf("[%s] Relaunching game after RUN -> UI regression (attempt %d/%d)",
					b.name, relaunchAttempts, MaxLobbyRelaunchAttempts)
				launched = true
				if !timeoutTimer.Stop() {
					select {
					case <-timeoutTimer.C:
					default:
					}
				}
				b.dota2Client.LaunchLobby()
				log.Printf("[%s] Relaunch command sent!", b.name)
			} else {
				log.Printf("[%s] Skipping relaunch attempt %d/%d: players not in correct teams",
					b.name, relaunchAttempts, MaxLobbyRelaunchAttempts)
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
