package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/edvart/dota-inhouse/internal/dotaapi"
	"github.com/golang/protobuf/proto"
	"github.com/paralin/go-dota2"
	"github.com/paralin/go-dota2/cso"
	devents "github.com/paralin/go-dota2/events"
	"github.com/paralin/go-dota2/protocol"
	"github.com/paralin/go-steam"
	"github.com/paralin/go-steam/protocol/steamlang"
	"github.com/paralin/go-steam/steamid"
	"github.com/sirupsen/logrus"
)

type Bot struct {
	name        string
	client      *steam.Client
	dota2Client *dota2.Dota2
	realtime    *dotaapi.RealtimeClient
	loggedIn    bool
	busy        bool
	gcReady     bool
	gcReadyCh   chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
	retryAfter  time.Duration
	mu          sync.Mutex
}

type Status struct {
	Name          string
	LoggedIn      bool
	Busy          bool
	HasDotaClient bool
	State         string
}

const gcMsgLiveScoreboardUpdate = uint32(protocol.EDOTAGCMsg_k_EMsgGCLiveScoreboardUpdate)

var initSteamDirectoryOnce sync.Once

func NewBot(username, password string, initialDelay time.Duration, steamAPIKey string) *Bot {
	initSteamDirectoryOnce.Do(func() {
		if err := steam.InitializeSteamDirectory(); err != nil {
			log.Printf("Failed to initialize Steam Directory (using static CM list): %v", err)
		} else {
			log.Printf("Initialized Steam Directory CM list")
		}
	})

	bot := &Bot{
		name:      username,
		client:    steam.NewClient(),
		gcReadyCh: make(chan struct{}),
	}
	if steamAPIKey != "" {
		bot.realtime = dotaapi.NewRealtimeClient(steamAPIKey)
	}

	loginInfo := &steam.LogOnDetails{
		Username:               username,
		Password:               password,
		DeviceFriendlyName:     "dota-inhouse",
		ShouldRememberPassword: true,
	}

	go func() {
		if initialDelay > 0 {
			log.Printf("[%s] Waiting %v before initial Steam login", bot.name, initialDelay)
			time.Sleep(initialDelay)
		}
		bot.connectWithRetry(loginInfo, 20*time.Second)
	}()

	return bot
}

func (b *Bot) connectWithRetry(loginInfo *steam.LogOnDetails, timeout time.Duration) {
	attempt := 0
	for {
		attempt++
		log.Printf("[%s] Connection attempt %d", b.name, attempt)

		var retryAfter time.Duration
		firstConnected := b.attemptConnection(timeout)
		if firstConnected != nil {
			log.Printf("[%s] Connection established, listening to events", b.name)
			wasLoggedIn := b.handleEvents(loginInfo, firstConnected)
			b.mu.Lock()
			retryAfter = b.retryAfter
			b.retryAfter = 0
			b.mu.Unlock()
			// If handleEvents returns, the connection was lost - reconnect
			log.Printf("[%s] Connection lost, will reconnect...", b.name)
			if wasLoggedIn {
				attempt = 0 // Reset only after an authenticated session.
			}
		}

		// Calculate backoff: 5s, 10s, 15s, ... up to 60s max.
		// After any disconnect, wait at least 5s to avoid reconnect storms.
		retryAttempt := attempt
		if retryAttempt < 1 {
			retryAttempt = 1
		}
		backoff := time.Duration(retryAttempt) * 5 * time.Second
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}
		if retryAfter > backoff {
			backoff = retryAfter
		}
		log.Printf("[%s] Connection failed, retrying in %v...", b.name, backoff)
		time.Sleep(backoff)

		b.mu.Lock()
		b.client = steam.NewClient()
		b.loggedIn = false
		b.dota2Client = nil
		b.markDotaGCNotReadyLocked()
		b.mu.Unlock()
	}
}

func (b *Bot) attemptConnection(timeout time.Duration) *steam.ConnectedEvent {
	cm := b.client.Connect()
	if cm != nil {
		log.Printf("[%s] Connecting to CM %s", b.name, cm.String())
	}

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
			case error:
				log.Printf("[%s] Pre-connect error: %v", b.name, e)
			default:
				log.Printf("[%s] Ignoring pre-connect event %T while waiting for ConnectedEvent", b.name, event)
			}
		}
	}
}

func (b *Bot) handleEvents(loginInfo *steam.LogOnDetails, firstEvent interface{}) (wasLoggedIn bool) {
	const logonTimeout = 45 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.ctx = ctx
	b.cancel = cancel
	b.mu.Unlock()
	defer cancel()

	log.Printf("[%s] Listening to steam client events", b.name)

	if b.processEvent(ctx, firstEvent, loginInfo) {
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

			if b.processEvent(ctx, event, loginInfo) {
				return
			}
			if waitingForLogon {
				if _, ok := event.(*steam.LoggedOnEvent); ok {
					wasLoggedIn = true
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

func (b *Bot) processEvent(ctx context.Context, event interface{}, loginInfo *steam.LogOnDetails) bool {
	switch e := event.(type) {
	case *steam.ConnectedEvent:
		log.Printf("[%s] Connected, logging on…", b.name)
		if err := b.client.Auth.LogOn(ctx, loginInfo); err != nil {
			log.Printf("[%s] Steam authentication failed: %v", b.name, err)
			if strings.Contains(err.Error(), "eresult 87") {
				b.mu.Lock()
				b.retryAfter = 15 * time.Minute
				b.mu.Unlock()
				log.Printf("[%s] Steam login is throttled; pausing before retry", b.name)
			}
			b.client.Disconnect()
			return true
		}
		return false

	case *steam.LoggedOnEvent:
		b.mu.Lock()
		b.loggedIn = true
		b.mu.Unlock()
		log.Printf("[%s] Logged on successfully!", b.name)
		return false

	case *steam.LogOnFailedEvent:
		if e.Err != nil {
			log.Printf("[%s] Logon failed during %s: %v (available confirmations: %v)", b.name, e.AuthSessionState, e.Err, e.Confirmations)
		} else {
			log.Printf("[%s] Logon failed: %v", b.name, e.Result)
		}
		return false

	case *steam.SteamFailureEvent:
		log.Printf("[%s] Steam failure reported: %v", b.name, e.Result)
		return false

	case *steam.LoggedOffEvent:
		log.Printf("[%s] Logged off: %v", b.name, e.Result)
		return false

	case *steam.DisconnectedEvent:
		log.Printf("[%s] Disconnected.", b.name)
		b.mu.Lock()
		b.loggedIn = false
		dotaClient := b.dota2Client
		b.dota2Client = nil
		b.markDotaGCNotReadyLocked()
		b.mu.Unlock()
		if dotaClient != nil {
			dotaClient.SetPlaying(false)
			dotaClient.Close()
		}
		// Exit the event loop so connectWithRetry can establish a fresh connection.
		return true

	case *devents.ClientWelcomed:
		log.Printf("[%s] Dota GC session ready", b.name)
		b.mu.Lock()
		b.markDotaGCReadyLocked()
		b.mu.Unlock()
		return false

	case *devents.GCConnectionStatusChanged:
		log.Printf("[%s] Dota GC connection status changed: %v -> %v", b.name, e.OldState, e.NewState)
		if e.NewState != protocol.GCConnectionStatus_GCConnectionStatus_HAVE_SESSION {
			b.mu.Lock()
			b.markDotaGCNotReadyLocked()
			b.mu.Unlock()
		}
		return false

	case *devents.UnhandledGCPacket:
		b.handleUnhandledGCPacket(e)
		return false

	case error:
		log.Printf("[%s] Steam client error event: %v", b.name, e)
		return false
	}

	return false
}

func (b *Bot) markDotaGCReadyLocked() {
	if b.gcReady {
		return
	}
	b.gcReady = true
	close(b.gcReadyCh)
}

func (b *Bot) markDotaGCNotReadyLocked() {
	if b.gcReadyCh == nil {
		b.gcReadyCh = make(chan struct{})
	}
	if !b.gcReady {
		return
	}
	b.gcReady = false
	b.gcReadyCh = make(chan struct{})
}

func (b *Bot) handleUnhandledGCPacket(event *devents.UnhandledGCPacket) {
	if event == nil || event.Packet == nil {
		return
	}

	packet := event.Packet
	if packet.MsgType != gcMsgLiveScoreboardUpdate {
		return
	}

	update := &protocol.CMsgDOTALiveScoreboardUpdate{}
	if err := proto.Unmarshal(packet.Body, update); err != nil {
		log.Printf("[%s] Failed to decode live scoreboard packet: %v", b.name, err)
		return
	}

	teamGood := update.GetTeamGood()
	teamBad := update.GetTeamBad()

	radiantScore := uint32(0)
	if teamGood != nil {
		radiantScore = teamGood.GetScore()
	}
	direScore := uint32(0)
	if teamBad != nil {
		direScore = teamBad.GetScore()
	}

	radiantPlayers := 0
	if teamGood != nil {
		radiantPlayers = len(teamGood.GetPlayers())
	}
	direPlayers := 0
	if teamBad != nil {
		direPlayers = len(teamBad.GetPlayers())
	}

	log.Printf(
		"[%s] GC LIVE SCOREBOARD packet: match_id=%d game_time=%.0fs score=%d-%d players=%d:%d",
		b.name,
		update.GetMatchId(),
		update.GetDuration(),
		radiantScore,
		direScore,
		radiantPlayers,
		direPlayers,
	)
}

func (b *Bot) IsAvailable() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.loggedIn && !b.busy
}

func (b *Bot) IsLoggedIn() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.loggedIn
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

func (b *Bot) NotifyMatchAccept(matchID string, players []coordinator.Player, baseURL string) {
	siteURL := strings.TrimRight(baseURL, "/")
	if siteURL == "" {
		siteURL = "/"
	}

	message := fmt.Sprintf("DNDL match found. Accept here: %s", siteURL)

	b.mu.Lock()
	client := b.client
	loggedIn := b.loggedIn
	botName := b.name
	b.mu.Unlock()

	if !loggedIn || client == nil {
		log.Printf("[%s] Skipping Steam accept notifications for match %s: bot is not logged in", botName, matchID)
		return
	}

	for _, player := range players {
		id, err := strconv.ParseUint(player.SteamID, 10, 64)
		if err != nil {
			log.Printf("[%s] Skipping Steam accept notification for %s in match %s: invalid SteamID %q: %v", botName, player.Name, matchID, player.SteamID, err)
			continue
		}

		steamID := steamid.SteamId(id)
		client.Social.AddFriend(steamID)
		client.Social.SendMessage(steamID, steamlang.EChatEntryType_ChatMsg, message)
		log.Printf("[%s] Attempted Steam friend request and accept DM to %s (%s) for match %s", botName, player.Name, player.SteamID, matchID)
	}
}

const (
	DotaGCReadyTimeout       = 15 * time.Second
	LobbyCreationTimeout     = 15 * time.Second
	LobbyDestroyTimeout      = 5 * time.Second
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

func (b *Bot) CreateLobby(ctx context.Context, matchID string, players []coordinator.Player, radiant []coordinator.Player, dire []coordinator.Player, gameMode string, leagueID uint32, lobbyDeadline time.Time, lobbyTimerControls <-chan lobbyTimerControl, commands chan<- coordinator.Command) bool {
	b.mu.Lock()
	if !b.loggedIn {
		b.mu.Unlock()
		log.Printf("[%s] Cannot create lobby: not logged in", b.name)
		return false
	}
	b.busy = true
	sessionCtx := b.ctx
	dotaClient := b.dota2Client
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.busy = false
		b.mu.Unlock()
	}()

	if sessionCtx == nil {
		log.Printf("[%s] Cannot create lobby: steam session context unavailable", b.name)
		return false
	}

	if dotaClient == nil {
		log.Printf("[%s] Creating new Dota 2 client", b.name)
		logger := logrus.New()
		logger.SetLevel(logrus.WarnLevel)
		dotaClient = dota2.New(b.client, logger)
		b.mu.Lock()
		b.dota2Client = dotaClient
		b.markDotaGCNotReadyLocked()
		b.mu.Unlock()
	} else {
		log.Printf("[%s] Reusing existing Dota 2 client", b.name)
	}

	if err := b.waitForDotaGCReady(ctx, sessionCtx, dotaClient); err != nil {
		log.Printf("[%s] Cannot create lobby: Dota GC not ready: %v", b.name, err)
		if ctx.Err() == nil && sessionCtx.Err() == nil {
			b.reconnectAfterDotaFailure(dotaClient, "Dota GC readiness failure")
		}
		return false
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

	creationCtx, creationCancel := context.WithTimeout(ctx, LobbyCreationTimeout)
	defer creationCancel()
	stopCreationOnSessionEnd := cancelWhenDone(creationCancel, sessionCtx)
	err := dotaClient.LeaveCreateLobby(creationCtx, lobbyDetails, true)
	stopCreationOnSessionEnd()
	if err != nil {
		log.Printf("[%s] Failed to create lobby for match %s within %v: %v", b.name, matchID, LobbyCreationTimeout, err)
		b.destroyLobbyWithTimeout(sessionCtx, dotaClient, "failed lobby creation")
		if ctx.Err() == nil && sessionCtx.Err() == nil {
			b.reconnectAfterDotaFailure(dotaClient, "failed lobby creation")
		}
		return false
	}

	log.Printf("[%s] Moving bot to unassigned pool", b.name)
	dotaClient.JoinLobbyTeam(protocol.DOTA_GC_TEAM_DOTA_GC_TEAM_PLAYER_POOL, 1)
	time.Sleep(time.Second)

	log.Printf("[%s] Inviting players", b.name)
	for _, player := range players {
		id, err := strconv.ParseUint(player.SteamID, 10, 64)
		if err == nil {
			dotaClient.InviteLobbyMember(steamid.SteamId(id))
			log.Printf("[%s] Invited player: %s", b.name, player.Name)
		} else {
			log.Printf("[%s] Invalid steam ID for player %s: %v", b.name, player.Name, err)
		}
	}

	commands <- coordinator.BotLobbyReady{MatchID: matchID}

	b.monitorLobbyState(ctx, sessionCtx, dotaClient, matchID, radiant, dire, lobbyDeadline, lobbyTimerControls, commands)
	return true
}

func (b *Bot) waitForDotaGCReady(ctx context.Context, sessionCtx context.Context, dotaClient *dota2.Dota2) error {
	b.mu.Lock()
	if b.gcReady {
		b.mu.Unlock()
		return nil
	}
	readyCh := b.gcReadyCh
	b.mu.Unlock()

	log.Printf("[%s] Waiting for Dota GC session", b.name)
	dotaClient.SetPlaying(true)
	dotaClient.SayHello()

	timeout := time.NewTimer(DotaGCReadyTimeout)
	defer timeout.Stop()

	retryHello := time.NewTicker(5 * time.Second)
	defer retryHello.Stop()

	for {
		select {
		case <-readyCh:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-sessionCtx.Done():
			return sessionCtx.Err()
		case <-timeout.C:
			return fmt.Errorf("timed out after %v", DotaGCReadyTimeout)
		case <-retryHello.C:
			log.Printf("[%s] Still waiting for Dota GC session, sending hello again", b.name)
			dotaClient.SetPlaying(true)
			dotaClient.SayHello()
		}
	}
}

func cancelWhenDone(cancel context.CancelFunc, watched context.Context) context.CancelFunc {
	done := make(chan struct{})
	go func() {
		select {
		case <-watched.Done():
			cancel()
		case <-done:
		}
	}()
	return func() {
		close(done)
	}
}

func (b *Bot) destroyLobbyWithTimeout(sessionCtx context.Context, dotaClient *dota2.Dota2, reason string) {
	destroyCtx, destroyCancel := context.WithTimeout(context.Background(), LobbyDestroyTimeout)
	defer destroyCancel()
	stopDestroyOnSessionEnd := cancelWhenDone(destroyCancel, sessionCtx)
	defer stopDestroyOnSessionEnd()

	if _, err := dotaClient.DestroyLobby(destroyCtx); err != nil {
		log.Printf("[%s] Failed to destroy lobby after %s: %v", b.name, reason, err)
		return
	}
	log.Printf("[%s] Destroyed lobby after %s", b.name, reason)
}

func (b *Bot) reconnectAfterDotaFailure(dotaClient *dota2.Dota2, reason string) {
	b.mu.Lock()
	client := b.client
	if b.dota2Client == dotaClient {
		b.dota2Client = nil
	}
	b.loggedIn = false
	b.markDotaGCNotReadyLocked()
	b.mu.Unlock()

	log.Printf("[%s] Reconnecting Steam client after %s to reset Dota GC state", b.name, reason)

	if dotaClient != nil {
		dotaClient.SetPlaying(false)
		dotaClient.Close()
	}
	if client != nil {
		client.Disconnect()
	}
}

func (b *Bot) monitorLobbyState(ctx context.Context, sessionCtx context.Context, dotaClient *dota2.Dota2, matchID string, expectedRadiant []coordinator.Player, expectedDire []coordinator.Player, lobbyDeadline time.Time, lobbyTimerControls <-chan lobbyTimerControl, commands chan<- coordinator.Command) {
	eventCh, eventCancel, err := dotaClient.GetCache().SubscribeType(cso.Lobby)
	if err != nil {
		log.Printf("[%s] Failed to subscribe to lobby events: %v", b.name, err)
		// Clean up the lobby we created
		b.destroyLobbyWithTimeout(sessionCtx, dotaClient, "lobby event subscription failure")
		// Notify coordinator that lobby failed (no players joined)
		commands <- coordinator.BotLobbyTimeout{
			MatchID:            matchID,
			PlayersJoinedRight: []string{},
		}
		if ctx.Err() == nil && sessionCtx.Err() == nil {
			b.reconnectAfterDotaFailure(dotaClient, "lobby event subscription failure")
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
	lastRunHeroLogKey := ""
	runHeroSnapshotLogged := false
	lastGameState := protocol.DOTA_GameState(-1)
	serverProbeLogged := false

	timeoutLobby := func(reason string) {
		joinedCorrectly := b.getCorrectlyJoinedPlayers(currentLobby, expectedTeam)
		commands <- coordinator.BotLobbyTimeout{
			MatchID:            matchID,
			Deadline:           lobbyDeadline,
			PlayersJoinedRight: joinedCorrectly,
		}
		b.destroyLobbyWithTimeout(sessionCtx, dotaClient, reason)
	}

	var timeoutTimer *time.Timer
	var timeoutTimerCh <-chan time.Time
	stopLobbyTimer := func() {
		if timeoutTimer == nil {
			return
		}
		if !timeoutTimer.Stop() {
			select {
			case <-timeoutTimerCh:
			default:
			}
		}
		timeoutTimer = nil
		timeoutTimerCh = nil
	}
	startLobbyTimer := func(deadline time.Time) bool {
		stopLobbyTimer()
		timeoutDuration := time.Until(deadline)
		if timeoutDuration <= 0 {
			return false
		}
		timeoutTimer = time.NewTimer(timeoutDuration)
		timeoutTimerCh = timeoutTimer.C
		log.Printf("[%s] Started lobby join timer for match %s (deadline: %s, remaining: %v)", b.name, matchID, deadline.Format(time.RFC3339), timeoutDuration)
		return true
	}
	defer stopLobbyTimer()
	defer func() {
		if relaunchTimer != nil {
			relaunchTimer.Stop()
		}
	}()

	lobbyPaused := false
	for {
		select {
		case control := <-lobbyTimerControls:
			if control.Paused {
				lobbyPaused = true
				continue
			}
			lobbyPaused = false
			lobbyDeadline = control.Deadline
		default:
			goto lobbyTimerReady
		}
	}

lobbyTimerReady:
	if lobbyPaused {
		log.Printf("[%s] Lobby join timer for match %s is paused", b.name, matchID)
	} else if !startLobbyTimer(lobbyDeadline) {
		log.Printf("[%s] Lobby join deadline already passed", b.name)
		timeoutLobby("lobby join deadline passed")
		return
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s] Lobby monitoring cancelled", b.name)
			return

		case <-sessionCtx.Done():
			log.Printf("[%s] Lobby monitoring stopped: steam session ended", b.name)
			return

		case <-timeoutTimerCh:
			timeoutTimer = nil
			timeoutTimerCh = nil
			if !launched && !gameEnded {
				log.Printf("[%s] Lobby join timeout reached", b.name)
				timeoutLobby("lobby join timeout")
				return
			}

		case control := <-lobbyTimerControls:
			if control.Paused {
				lobbyPaused = true
				stopLobbyTimer()
				log.Printf("[%s] Lobby join timer paused for match %s", b.name, matchID)
				continue
			}

			lobbyPaused = false
			lobbyDeadline = control.Deadline
			if !startLobbyTimer(lobbyDeadline) {
				log.Printf("[%s] Lobby join deadline already passed after resume", b.name)
				timeoutLobby("lobby join deadline passed after resume")
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

			if !serverProbeLogged && dota2Lobby.GetServerId() != 0 {
				serverProbeLogged = true
				b.startLiveDataProbe(ctx, dota2Lobby, matchID)
			}

			if gameState := dota2Lobby.GetGameState(); gameState != lastGameState {
				lastGameState = gameState
				log.Printf("[%s] Lobby game_state for match %s: %v", b.name, matchID, gameState)
			}

			if currentState == protocol.CSODOTALobby_RUN {
				heroLogKey := formatLobbyHeroIDs(dota2Lobby)
				if !runHeroSnapshotLogged {
					runHeroSnapshotLogged = true
					lastRunHeroLogKey = heroLogKey
					log.Printf("[%s] RUN lobby initial hero IDs for match %s: %s", b.name, matchID, heroLogKey)
				} else if heroLogKey != lastRunHeroLogKey {
					lastRunHeroLogKey = heroLogKey
					log.Printf("[%s] RUN lobby hero IDs changed for match %s: %s", b.name, matchID, heroLogKey)
				}
			}

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
						b.destroyLobbyWithTimeout(sessionCtx, dotaClient, "game ended")
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
					stopLobbyTimer()
					dotaClient.LaunchLobby()
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
				stopLobbyTimer()
				dotaClient.LaunchLobby()
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

// Live data probe tuning. Temporary: remove along with the probe once we know
// whether lobby server_id feeds GetRealtimeStats.
const (
	liveProbeInterval    = 10 * time.Second
	liveProbeMaxDuration = 2 * time.Hour
	// Valve returns 400 once a server is deallocated, so a short run of them
	// means the match is over rather than that the endpoint is broken.
	liveProbeMaxUnavailable = 3
)

// startLiveDataProbe logs the identifiers needed to evaluate live hero-pick
// sources, then polls GetRealtimeStats for the life of the match. It only
// logs; nothing is persisted or sent to the coordinator.
func (b *Bot) startLiveDataProbe(ctx context.Context, dota2Lobby *protocol.CSODOTALobby, matchID string) {
	serverID := dota2Lobby.GetServerId()

	log.Printf(
		"[%s] LIVE PROBE match=%s lobby_id=%d server_id=%d dota_match_id=%d lobby_state=%v game_state=%v",
		b.name,
		matchID,
		dota2Lobby.GetLobbyId(),
		serverID,
		dota2Lobby.GetMatchId(),
		dota2Lobby.GetState(),
		dota2Lobby.GetGameState(),
	)

	// Gameserver SteamIDs observed from Valve's live APIs all fall in the
	// 90xxxxxxxxxxxxxxx range. Anything else means server_id is not what
	// GetRealtimeStats wants.
	if serverID < 90000000000000000 || serverID > 91000000000000000 {
		log.Printf("[%s] LIVE PROBE server_id=%d is outside the expected gameserver SteamID range, not polling", b.name, serverID)
		return
	}

	if b.realtime == nil {
		log.Printf("[%s] LIVE PROBE skipping poll for match %s: no Steam API key configured", b.name, matchID)
		return
	}

	go b.pollRealtimeStats(ctx, serverID, matchID)
}

func (b *Bot) pollRealtimeStats(ctx context.Context, serverID uint64, matchID string) {
	probeCtx, cancel := context.WithTimeout(ctx, liveProbeMaxDuration)
	defer cancel()

	ticker := time.NewTicker(liveProbeInterval)
	defer ticker.Stop()

	log.Printf("[%s] LIVE PROBE polling GetRealtimeStats for match %s every %v", b.name, matchID, liveProbeInterval)

	unavailable := 0
	for {
		stats, err := b.realtime.GetRealtimeStats(probeCtx, serverID)
		switch {
		case err == nil:
			unavailable = 0
			log.Printf("[%s] LIVE PROBE %s", b.name, formatRealtimeStats(matchID, stats))

		case errors.Is(err, dotaapi.ErrRealtimeUnavailable):
			unavailable++
			log.Printf("[%s] LIVE PROBE match %s: server unavailable (%d/%d)", b.name, matchID, unavailable, liveProbeMaxUnavailable)
			if unavailable >= liveProbeMaxUnavailable {
				log.Printf("[%s] LIVE PROBE stopping for match %s: server is gone", b.name, matchID)
				return
			}

		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			log.Printf("[%s] LIVE PROBE stopping for match %s: %v", b.name, matchID, err)
			return

		default:
			log.Printf("[%s] LIVE PROBE match %s: %v", b.name, matchID, err)
		}

		select {
		case <-probeCtx.Done():
			log.Printf("[%s] LIVE PROBE stopping for match %s", b.name, matchID)
			return
		case <-ticker.C:
		}
	}
}

func formatRealtimeStats(matchID string, stats *dotaapi.RealtimeStats) string {
	m := stats.Match

	heroes := stats.HeroesByAccountID()
	accountIDs := make([]uint32, 0, len(heroes))
	for accountID := range heroes {
		accountIDs = append(accountIDs, accountID)
	}
	sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i] < accountIDs[j] })

	heroPairs := make([]string, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		heroPairs = append(heroPairs, fmt.Sprintf("%d=%d", accountID, heroes[accountID]))
	}

	return fmt.Sprintf(
		"match=%s dota_match_id=%d game_state=%v game_time=%d mode=%d lobby_type=%d league=%d picked=%d/%d picks=%s bans=%s heroes=[%s]",
		matchID,
		m.MatchID,
		protocol.DOTA_GameState(m.GameState), //nolint:gosec // enum is uint32 on the wire
		m.GameTime,
		m.GameMode,
		m.LobbyType,
		m.LeagueID,
		stats.PickedCount(),
		len(heroes),
		formatPickBans(m.Picks),
		formatPickBans(m.Bans),
		strings.Join(heroPairs, ", "),
	)
}

// formatPickBans renders the draft in the order Valve returned it, as
// hero:team pairs, so we can see whether order and team attribution survive.
func formatPickBans(entries []dotaapi.PickBan) string {
	if len(entries) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, fmt.Sprintf("%d:%d", e.Hero, e.Team))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func formatLobbyHeroIDs(dota2Lobby *protocol.CSODOTALobby) string {
	if dota2Lobby == nil {
		return "none"
	}

	heroIDsBySteamID := make(map[uint64]int32)
	for _, member := range dota2Lobby.GetAllMembers() {
		heroID := member.GetHeroId()
		if heroID <= 0 {
			continue
		}
		heroIDsBySteamID[member.GetId()] = heroID
	}

	if len(heroIDsBySteamID) == 0 {
		return "none"
	}

	steamIDs := make([]uint64, 0, len(heroIDsBySteamID))
	for steamID := range heroIDsBySteamID {
		steamIDs = append(steamIDs, steamID)
	}
	sort.Slice(steamIDs, func(i, j int) bool {
		return steamIDs[i] < steamIDs[j]
	})

	parts := make([]string, 0, len(steamIDs))
	for _, steamID := range steamIDs {
		parts = append(parts, fmt.Sprintf("%d=%d", steamID, heroIDsBySteamID[steamID]))
	}
	return fmt.Sprintf("%d/%d [%s]", len(heroIDsBySteamID), len(dota2Lobby.GetAllMembers()), strings.Join(parts, ", "))
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
