package web

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/edvart/dota-inhouse/internal/coordinator"
)

type SSEClient struct {
	ID      string
	UserID  string
	Channel chan string
}

type SSEHub struct {
	clients     map[*SSEClient]bool
	mu          sync.RWMutex
	templates   *template.Template
	coordinator *coordinator.Coordinator
}

func NewSSEHub(templates *template.Template, coord *coordinator.Coordinator) *SSEHub {
	return &SSEHub{
		clients:     make(map[*SSEClient]bool),
		templates:   templates,
		coordinator: coord,
	}
}

func (h *SSEHub) Run(events <-chan coordinator.Event) {
	log.Println("SSE hub started")
	for event := range events {
		h.broadcast(event)
	}
}

func (h *SSEHub) broadcast(event coordinator.Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var matchesHTML string
	if h.isMatchEvent(event) {
		matchesHTML = h.renderActiveMatches()
	}

	for client := range h.clients {
		html := h.renderEventForUser(event, client.UserID) + matchesHTML
		if html == "" {
			continue
		}
		select {
		case client.Channel <- html:
		default:
			log.Printf("Dropping message for slow client %s", client.ID)
		}
	}
}

func (h *SSEHub) isMatchEvent(event coordinator.Event) bool {
	switch event.(type) {
	case coordinator.MatchAcceptStarted,
		coordinator.MatchAcceptUpdated,
		coordinator.SideChoiceStarted,
		coordinator.DraftStarted,
		coordinator.DraftUpdated,
		coordinator.MatchCancelled,
		coordinator.DraftCancelled,
		coordinator.LobbyCancelled,
		coordinator.RequestBotLobby,
		coordinator.LobbyCountdownPaused,
		coordinator.LobbyCountdownResumed,
		coordinator.MatchStarted,
		coordinator.LiveHeroesUpdated,
		coordinator.MatchCompleted:
		return true
	default:
		return false
	}
}

func (h *SSEHub) renderEventForUser(event coordinator.Event, userID string) string {
	switch e := event.(type) {
	case coordinator.LobbySettingsUpdated:
		return h.renderCurrentQueuePanel(userID, h.inMatch(userID), false)

	case coordinator.QueueUpdated:
		includeActions := e.ActionsForAll || (userID != "" && slices.Contains(e.ActionPlayerIDs, userID))
		return h.renderQueuePanel(e.Queue, userID, h.inMatch(userID), includeActions)

	case coordinator.MatchAcceptStarted:
		if !hasPlayer(e.Players, userID) {
			return ""
		}
		return h.renderMatchArea(&coordinator.Match{
			ID:              e.MatchID,
			State:           coordinator.MatchStateAccepting,
			Players:         e.Players,
			AcceptedPlayers: map[string]bool{},
			AcceptDeadline:  e.Deadline,
		}, userID)

	case coordinator.MatchAcceptUpdated:
		match := h.coordinator.GetPlayerMatch(userID)
		if match == nil || match.ID != e.MatchID {
			return ""
		}
		data := AcceptDialogData{
			MatchID:      e.MatchID,
			Players:      match.Players,
			Accepted:     e.Accepted,
			Count:        len(e.Accepted),
			Total:        coordinator.MaxPlayers,
			UserID:       userID,
			UserAccepted: e.Accepted[userID],
		}
		status := h.renderFragment("accept-status", data)
		if status == "" || e.PlayerID != userID {
			return status
		}
		button := h.renderFragment("accept-button", data)
		if button == "" {
			return ""
		}
		return status + button

	case coordinator.SideChoiceStarted:
		if !hasPlayer(e.Players, userID) {
			return ""
		}
		return h.renderMatchArea(&coordinator.Match{
			ID:                 e.MatchID,
			State:              coordinator.MatchStateChoosingSide,
			Captains:           e.Captains,
			SideChooserIndex:   e.SideChooserIndex,
			AvailablePlayers:   e.Available,
			SideChoiceDeadline: e.Deadline,
		}, userID)

	case coordinator.DraftStarted:
		if !hasPlayer(e.Radiant, userID) && !hasPlayer(e.Dire, userID) && !hasPlayer(e.Available, userID) {
			return ""
		}
		return h.renderMatchArea(&coordinator.Match{
			ID:               e.MatchID,
			State:            coordinator.MatchStateDrafting,
			Captains:         e.Captains,
			Radiant:          e.Radiant,
			Dire:             e.Dire,
			AvailablePlayers: e.Available,
			PickDeadline:     e.Deadline,
		}, userID)

	case coordinator.DraftUpdated:
		if !hasPlayer(e.Radiant, userID) && !hasPlayer(e.Dire, userID) && !hasPlayer(e.AvailablePlayers, userID) {
			return ""
		}
		return h.renderMatchArea(&coordinator.Match{
			ID:               e.MatchID,
			State:            coordinator.MatchStateDrafting,
			Captains:         e.Captains,
			Radiant:          e.Radiant,
			Dire:             e.Dire,
			AvailablePlayers: e.AvailablePlayers,
			CurrentPicker:    e.CurrentPicker,
			PickDeadline:     e.Deadline,
		}, userID)

	case coordinator.MatchCancelled:
		match := h.coordinator.GetPlayerMatch(userID)
		if match != nil && match.ID != e.MatchID {
			return ""
		}
		return h.renderFragment("match-cancelled", e)

	case coordinator.DraftCancelled:
		returned := hasPlayer(e.ReturnedToQueue, userID)
		if !returned && e.FailedCaptain.SteamID != userID {
			return ""
		}
		return h.renderWithQueue("draft-cancelled", e, userID, returned)

	case coordinator.LobbyCancelled:
		returned := hasPlayer(e.ReturnedToQueue, userID)
		if !returned && !hasPlayer(e.FailedPlayers, userID) {
			return ""
		}
		return h.renderWithQueue("lobby-cancelled", e, userID, returned)

	case coordinator.RequestBotLobby:
		if !hasPlayer(e.Players, userID) {
			return ""
		}
		return h.renderMatchArea(&coordinator.Match{
			ID:            e.MatchID,
			State:         coordinator.MatchStateWaitingForBot,
			LobbyDeadline: e.Deadline,
		}, userID)

	case coordinator.LobbyCountdownPaused:
		if !hasPlayer(e.Players, userID) {
			return ""
		}
		return h.renderMatchArea(&coordinator.Match{
			ID:             e.MatchID,
			State:          coordinator.MatchStateWaitingForBot,
			LobbyDeadline:  time.Now().Add(e.Remaining),
			LobbyPaused:    true,
			LobbyRemaining: e.Remaining,
		}, userID)

	case coordinator.LobbyCountdownResumed:
		if !hasPlayer(e.Players, userID) {
			return ""
		}
		return h.renderMatchArea(&coordinator.Match{
			ID:            e.MatchID,
			State:         coordinator.MatchStateWaitingForBot,
			LobbyDeadline: e.Deadline,
		}, userID)

	case coordinator.MatchStarted:
		if !hasPlayer(e.Players, userID) {
			return ""
		}
		startedAt := e.StartedAt
		return h.renderMatchArea(&coordinator.Match{
			ID:            e.MatchID,
			State:         coordinator.MatchStateInProgress,
			DotaMatchID:   e.DotaMatchID,
			GameStartedAt: &startedAt,
		}, userID)

	case coordinator.MatchCompleted:
		if !hasPlayer(e.Players, userID) {
			return ""
		}
		return h.renderWithQueue("match-completed", e, userID, true)

	case coordinator.MatchCancelledByAdmin:
		if !hasPlayer(e.Players, userID) {
			return ""
		}
		data := struct {
			MatchID string
			Message string
		}{
			MatchID: e.MatchID,
			Message: "Match was cancelled by admin.",
		}
		return h.renderWithQueue("admin-match-cancelled", data, userID, e.ReturnedToQueue)

	default:
		return ""
	}
}

func (h *SSEHub) renderWithQueue(name string, data any, userID string, includeQueue bool) string {
	html := h.renderFragment(name, data)
	if html == "" || !includeQueue {
		return html
	}
	return html + h.renderCurrentQueuePanel(userID, false, true)
}

func (h *SSEHub) renderFragment(name string, data any) string {
	var buf bytes.Buffer
	if err := h.templates.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("Failed to render %s: %v", name, err)
		return ""
	}
	return buf.String()
}

type QueuePanelData struct {
	Queue          []coordinator.Player
	InQueue        bool
	InMatch        bool
	QueueOpen      bool
	GameModeName   string
	IncludeActions bool
}

func (h *SSEHub) renderQueuePanel(queue []coordinator.Player, userID string, inMatch, includeActions bool) string {
	_, _, lobbySettings, queueOpen := h.coordinator.GetState()
	return h.renderFragment("queue-sse", QueuePanelData{
		Queue:          queue,
		InQueue:        hasPlayer(queue, userID),
		InMatch:        inMatch,
		QueueOpen:      queueOpen,
		GameModeName:   gameModeName(lobbySettings.GameMode),
		IncludeActions: includeActions,
	})
}

func (h *SSEHub) renderCurrentQueuePanel(userID string, inMatch, includeActions bool) string {
	queue, _, _, _ := h.coordinator.GetState()
	return h.renderQueuePanel(queue, userID, inMatch, includeActions)
}

func (h *SSEHub) inMatch(userID string) bool {
	return h.coordinator.GetPlayerMatch(userID) != nil
}

type AcceptDialogData struct {
	MatchID      string
	Players      []coordinator.Player
	Accepted     map[string]bool
	Deadline     string
	Count        int
	Total        int
	UserID       string
	UserAccepted bool
}

type WaitingForBotData struct {
	MatchID          string
	Message          string
	Deadline         string
	Paused           bool
	RemainingSeconds int
}

type MatchInProgressData struct {
	MatchID     string
	DotaMatchID uint64
	StartedAt   string
}

type DraftData struct {
	MatchID          string
	Captains         [2]coordinator.Player
	AvailablePlayers []coordinator.Player
	Radiant          []coordinator.Player
	Dire             []coordinator.Player
	CurrentPicker    int
	Deadline         string
}

type SideChoiceData struct {
	MatchID          string
	Captains         [2]coordinator.Player
	SideChooserIndex int
	AvailablePlayers []coordinator.Player
	Deadline         string
	UserID           string
}

func (h *SSEHub) renderInitialState(userID string) string {
	queue, matches, _, _ := h.coordinator.GetState()

	queueHTML := h.renderQueuePanel(queue, userID, h.inMatch(userID), true)
	if queueHTML == "" {
		return ""
	}
	matchesHTML := h.renderMatchList(matches)
	if matchesHTML == "" {
		return ""
	}
	return queueHTML + matchesHTML + h.renderCurrentMatchArea(userID)
}

func (h *SSEHub) renderCurrentMatchArea(userID string) string {
	if userID == "" {
		return ""
	}
	match := h.coordinator.GetPlayerMatch(userID)
	if match == nil {
		return h.renderFragment("empty-match-area", nil)
	}
	return h.renderMatchArea(match, userID)
}

func (h *SSEHub) renderMatchArea(match *coordinator.Match, userID string) string {
	switch match.State {
	case coordinator.MatchStateAccepting:
		return h.renderFragment("accept-dialog", AcceptDialogData{
			MatchID:      match.ID,
			Players:      match.Players,
			Accepted:     match.AcceptedPlayers,
			Deadline:     match.AcceptDeadline.Format(time.RFC3339),
			Count:        len(match.AcceptedPlayers),
			Total:        coordinator.MaxPlayers,
			UserID:       userID,
			UserAccepted: match.AcceptedPlayers[userID],
		})

	case coordinator.MatchStateChoosingSide:
		return h.renderFragment("side-choice", SideChoiceData{
			MatchID:          match.ID,
			Captains:         match.Captains,
			SideChooserIndex: match.SideChooserIndex,
			AvailablePlayers: match.AvailablePlayers,
			Deadline:         match.SideChoiceDeadline.Format(time.RFC3339),
			UserID:           userID,
		})

	case coordinator.MatchStateDrafting:
		return h.renderFragment("draft", DraftData{
			MatchID:          match.ID,
			Captains:         match.Captains,
			AvailablePlayers: match.AvailablePlayers,
			Radiant:          match.Radiant,
			Dire:             match.Dire,
			CurrentPicker:    match.CurrentPicker,
			Deadline:         match.PickDeadline.Format(time.RFC3339),
		})

	case coordinator.MatchStateWaitingForBot:
		return h.renderFragment("waiting-for-bot", WaitingForBotData{
			MatchID:          match.ID,
			Message:          "Waiting for Dota 2 lobby...",
			Deadline:         match.LobbyDeadline.Format(time.RFC3339),
			Paused:           match.LobbyPaused,
			RemainingSeconds: max(int(match.LobbyRemaining.Seconds()), 0),
		})

	case coordinator.MatchStateInProgress:
		data := MatchInProgressData{
			MatchID:     match.ID,
			DotaMatchID: match.DotaMatchID,
		}
		if match.GameStartedAt != nil {
			data.StartedAt = match.GameStartedAt.Format(time.RFC3339)
		}
		return h.renderFragment("match-in-progress", data)

	default:
		return ""
	}
}

func (h *SSEHub) renderActiveMatches() string {
	_, matches, _, _ := h.coordinator.GetState()
	return h.renderMatchList(matches)
}

func (h *SSEHub) renderMatchList(matches map[string]*coordinator.Match) string {
	return h.renderFragment("active-matches", struct {
		Matches []*coordinator.Match
	}{Matches: slices.Collect(maps.Values(matches))})
}

func (h *SSEHub) HandleConnection(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	// Disable buffering for Cloudflare/nginx proxies
	w.Header().Set("X-Accel-Buffering", "no")

	client := &SSEClient{
		ID:      fmt.Sprintf("%p", r),
		UserID:  userID,
		Channel: make(chan string, 10),
	}

	h.mu.Lock()
	h.clients[client] = true
	clientCount := len(h.clients)
	h.mu.Unlock()

	// SSE connect/disconnect logged only at high volume for debugging
	_ = clientCount

	// Ensure cleanup on disconnect
	defer func() {
		h.mu.Lock()
		delete(h.clients, client)
		clientCount := len(h.clients)
		h.mu.Unlock()
		close(client.Channel)
		_ = clientCount
	}()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	if initialHTML := h.renderInitialState(userID); initialHTML != "" {
		lines := strings.Split(initialHTML, "\n")
		for _, line := range lines {
			fmt.Fprintf(w, "data: %s\n", line)
		}
		fmt.Fprintf(w, "\n")
		flusher.Flush()
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case msg, ok := <-client.Channel:
			if !ok {
				return
			}
			lines := strings.Split(msg, "\n")
			for _, line := range lines {
				fmt.Fprintf(w, "data: %s\n", line)
			}
			fmt.Fprintf(w, "\n")
			flusher.Flush()
		}
	}
}

func hasPlayer(players []coordinator.Player, userID string) bool {
	return slices.ContainsFunc(players, func(p coordinator.Player) bool { return p.SteamID == userID })
}
