package web

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
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
		html := h.renderEventForUser(event, client.UserID)

		if html == "" && matchesHTML != "" {
			html = matchesHTML
		} else if html != "" && matchesHTML != "" {
			html = html + matchesHTML
		}

		if html == "" {
			continue
		}
		select {
		case client.Channel <- html:
		default:
			// Client too slow, skip
			log.Printf("Dropping message for slow client %s", client.ID)
		}
	}
}

func (h *SSEHub) isMatchEvent(event coordinator.Event) bool {
	switch event.(type) {
	case coordinator.MatchAcceptStarted,
		coordinator.MatchAcceptUpdated,
		coordinator.DraftStarted,
		coordinator.DraftUpdated,
		coordinator.MatchCancelled,
		coordinator.DraftCancelled,
		coordinator.LobbyCancelled,
		coordinator.RequestBotLobby,
		coordinator.MatchStarted,
		coordinator.MatchCompleted:
		return true
	default:
		return false
	}
}

func (h *SSEHub) renderEventForUser(event coordinator.Event, userID string) string {
	var buf bytes.Buffer

	switch e := event.(type) {
	case coordinator.LobbySettingsUpdated:
		queue, _, _, _ := h.coordinator.GetState()
		inMatch := h.coordinator.GetPlayerMatch(userID) != nil
		data := h.queuePanelData(queue, userID, inMatch)
		if err := h.templates.ExecuteTemplate(&buf, "queue-sse", data); err != nil {
			log.Printf("Failed to render queue after lobby settings update: %v", err)
			return ""
		}

	case coordinator.QueueUpdated:
		inMatch := h.coordinator.GetPlayerMatch(userID) != nil
		data := h.queuePanelData(e.Queue, userID, inMatch)
		if err := h.templates.ExecuteTemplate(&buf, "queue-sse", data); err != nil {
			log.Printf("Failed to render queue: %v", err)
			return ""
		}

	case coordinator.MatchAcceptStarted:
		// Only send to users in this match
		if !isUserInMatch(userID, e.Players) {
			return ""
		}
		data := struct {
			MatchID      string
			Players      []coordinator.Player
			Accepted     map[string]bool
			Deadline     string
			Count        int
			Total        int
			UserID       string
			UserAccepted bool
		}{
			MatchID:      e.MatchID,
			Players:      e.Players,
			Accepted:     make(map[string]bool),
			Deadline:     e.Deadline.Format(time.RFC3339),
			Count:        0,
			Total:        coordinator.MaxPlayers,
			UserID:       userID,
			UserAccepted: false,
		}
		if err := h.templates.ExecuteTemplate(&buf, "accept-dialog", data); err != nil {
			log.Printf("Failed to render accept dialog: %v", err)
			return ""
		}

	case coordinator.MatchAcceptUpdated:
		// Only send to users in the match (check via coordinator)
		match := h.coordinator.GetPlayerMatch(userID)
		if match == nil || match.ID != e.MatchID {
			return ""
		}
		userAccepted := e.Accepted[userID]
		data := struct {
			MatchID      string
			Players      []coordinator.Player
			Accepted     map[string]bool
			Count        int
			Total        int
			UserID       string
			UserAccepted bool
		}{
			MatchID:      e.MatchID,
			Players:      match.Players,
			Accepted:     e.Accepted,
			Count:        len(e.Accepted),
			Total:        coordinator.MaxPlayers,
			UserID:       userID,
			UserAccepted: userAccepted,
		}
		if err := h.templates.ExecuteTemplate(&buf, "accept-status", data); err != nil {
			log.Printf("Failed to render accept status: %v", err)
			return ""
		}

		if err := h.templates.ExecuteTemplate(&buf, "accept-button", data); err != nil {
			log.Printf("Failed to render accept button: %v", err)
			return ""
		}

	case coordinator.DraftStarted:
		// Only send to users in this match
		if !isUserInPlayers(userID, e.Radiant) && !isUserInPlayers(userID, e.Dire) && !isUserInPlayers(userID, e.Available) {
			return ""
		}
		data := DraftData{
			MatchID:          e.MatchID,
			Captains:         e.Captains,
			Radiant:          e.Radiant,
			Dire:             e.Dire,
			AvailablePlayers: e.Available,
			CurrentPicker:    0,
			Deadline:         e.Deadline.Format(time.RFC3339),
		}
		if err := h.templates.ExecuteTemplate(&buf, "draft", data); err != nil {
			log.Printf("Failed to render draft: %v", err)
			return ""
		}

	case coordinator.DraftUpdated:
		// Only send to users in this match
		if !isUserInPlayers(userID, e.Radiant) && !isUserInPlayers(userID, e.Dire) && !isUserInPlayers(userID, e.AvailablePlayers) {
			return ""
		}
		data := DraftData{
			MatchID:          e.MatchID,
			Captains:         e.Captains,
			AvailablePlayers: e.AvailablePlayers,
			Radiant:          e.Radiant,
			Dire:             e.Dire,
			CurrentPicker:    e.CurrentPicker,
			Deadline:         e.Deadline.Format(time.RFC3339),
		}
		if err := h.templates.ExecuteTemplate(&buf, "draft", data); err != nil {
			log.Printf("Failed to render draft: %v", err)
			return ""
		}

	case coordinator.MatchCancelled:
		// Players are already returned to queue, so check they're not in a different match
		match := h.coordinator.GetPlayerMatch(userID)
		if match != nil && match.ID != e.MatchID {
			return "" // User is in a different match
		}
		if err := h.templates.ExecuteTemplate(&buf, "match-cancelled", e); err != nil {
			log.Printf("Failed to render match cancelled: %v", err)
			return ""
		}

	case coordinator.DraftCancelled:
		// Send to users who were returned to queue (everyone except failed captain)
		wasInMatch := isUserInPlayers(userID, e.ReturnedToQueue) || e.FailedCaptain.SteamID == userID
		if !wasInMatch {
			return ""
		}
		if err := h.templates.ExecuteTemplate(&buf, "draft-cancelled", e); err != nil {
			log.Printf("Failed to render draft cancelled: %v", err)
			return ""
		}
		if isUserInPlayers(userID, e.ReturnedToQueue) {
			queue, _, _, _ := h.coordinator.GetState()
			queueData := h.queuePanelData(queue, userID, false)
			if err := h.templates.ExecuteTemplate(&buf, "queue-sse", queueData); err != nil {
				log.Printf("Failed to render queue after draft cancelled: %v", err)
			}
		}

	case coordinator.LobbyCancelled:
		wasInMatch := isUserInPlayers(userID, e.ReturnedToQueue) || isUserInPlayers(userID, e.FailedPlayers)
		if !wasInMatch {
			return ""
		}
		if err := h.templates.ExecuteTemplate(&buf, "lobby-cancelled", e); err != nil {
			log.Printf("Failed to render lobby cancelled: %v", err)
			return ""
		}
		if isUserInPlayers(userID, e.ReturnedToQueue) {
			queue, _, _, _ := h.coordinator.GetState()
			queueData := h.queuePanelData(queue, userID, false)
			if err := h.templates.ExecuteTemplate(&buf, "queue-sse", queueData); err != nil {
				log.Printf("Failed to render queue after lobby cancelled: %v", err)
			}
		}

	case coordinator.RequestBotLobby:
		// Only send to users in this match
		if !isUserInPlayers(userID, e.Players) {
			return ""
		}
		data := struct {
			MatchID  string
			Message  string
			Deadline string
		}{
			MatchID:  e.MatchID,
			Message:  "Waiting for Dota 2 lobby...",
			Deadline: e.Deadline.Format(time.RFC3339),
		}
		if err := h.templates.ExecuteTemplate(&buf, "waiting-for-bot", data); err != nil {
			log.Printf("Failed to render waiting: %v", err)
			return ""
		}

	case coordinator.MatchCompleted:
		if !isUserInPlayers(userID, e.Players) {
			return ""
		}
		if err := h.templates.ExecuteTemplate(&buf, "match-completed", e); err != nil {
			log.Printf("Failed to render match completed: %v", err)
			return ""
		}
		queue, _, _, _ := h.coordinator.GetState()
		queueData := h.queuePanelData(queue, userID, false)
		if err := h.templates.ExecuteTemplate(&buf, "queue-sse", queueData); err != nil {
			log.Printf("Failed to render queue after match completed: %v", err)
		}

	case coordinator.MatchCancelledByAdmin:
		if !isUserInPlayers(userID, e.Players) {
			return ""
		}
		data := struct {
			MatchID string
			Message string
		}{
			MatchID: e.MatchID,
			Message: "Match was cancelled by admin.",
		}
		if err := h.templates.ExecuteTemplate(&buf, "admin-match-cancelled", data); err != nil {
			log.Printf("Failed to render admin cancel: %v", err)
			return ""
		}
		if e.ReturnedToQueue {
			queue, _, _, _ := h.coordinator.GetState()
			queueData := h.queuePanelData(queue, userID, false)
			if err := h.templates.ExecuteTemplate(&buf, "queue-sse", queueData); err != nil {
				log.Printf("Failed to render queue after admin cancel: %v", err)
			}
		}

	default:
		return ""
	}

	return buf.String()
}

type QueuePanelData struct {
	Queue        []coordinator.Player
	InQueue      bool
	InMatch      bool
	QueueOpen    bool
	GameModeName string
}

func (h *SSEHub) queuePanelData(queue []coordinator.Player, userID string, inMatch bool) QueuePanelData {
	_, _, lobbySettings, queueOpen := h.coordinator.GetState()
	inQueue := false
	for _, p := range queue {
		if p.SteamID == userID {
			inQueue = true
			break
		}
	}
	return QueuePanelData{
		Queue:        queue,
		InQueue:      inQueue,
		InMatch:      inMatch,
		QueueOpen:    queueOpen,
		GameModeName: gameModeName(lobbySettings.GameMode),
	}
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

func (h *SSEHub) renderInitialState(userID string) string {
	queue, matches, _, _ := h.coordinator.GetState()

	inMatch := h.coordinator.GetPlayerMatch(userID) != nil

	matchList := make([]*coordinator.Match, 0, len(matches))
	for _, m := range matches {
		matchList = append(matchList, m)
	}

	var buf bytes.Buffer

	queueData := h.queuePanelData(queue, userID, inMatch)
	if err := h.templates.ExecuteTemplate(&buf, "queue-sse", queueData); err != nil {
		log.Printf("Failed to render initial queue: %v", err)
		return ""
	}

	matchesData := struct {
		Matches []*coordinator.Match
	}{Matches: matchList}
	if err := h.templates.ExecuteTemplate(&buf, "active-matches", matchesData); err != nil {
		log.Printf("Failed to render initial matches: %v", err)
		return ""
	}

	if matchHTML := h.renderCurrentMatchArea(userID); matchHTML != "" {
		buf.WriteString(matchHTML)
	}

	return buf.String()
}

func (h *SSEHub) renderCurrentMatchArea(userID string) string {
	if userID == "" {
		return ""
	}

	match := h.coordinator.GetPlayerMatch(userID)
	if match == nil {
		var buf bytes.Buffer
		if err := h.templates.ExecuteTemplate(&buf, "empty-match-area", nil); err != nil {
			log.Printf("Failed to render empty match area: %v", err)
			return ""
		}
		return buf.String()
	}

	var buf bytes.Buffer
	switch match.State {
	case coordinator.MatchStateAccepting:
		userAccepted := match.AcceptedPlayers[userID]
		data := struct {
			MatchID      string
			Players      []coordinator.Player
			Accepted     map[string]bool
			Deadline     string
			Count        int
			Total        int
			UserID       string
			UserAccepted bool
		}{
			MatchID:      match.ID,
			Players:      match.Players,
			Accepted:     match.AcceptedPlayers,
			Deadline:     match.AcceptDeadline.Format(time.RFC3339),
			Count:        len(match.AcceptedPlayers),
			Total:        coordinator.MaxPlayers,
			UserID:       userID,
			UserAccepted: userAccepted,
		}
		if err := h.templates.ExecuteTemplate(&buf, "accept-dialog", data); err != nil {
			log.Printf("Failed to render initial accept dialog: %v", err)
			return ""
		}

	case coordinator.MatchStateDrafting:
		data := DraftData{
			MatchID:          match.ID,
			Captains:         match.Captains,
			AvailablePlayers: match.AvailablePlayers,
			Radiant:          match.Radiant,
			Dire:             match.Dire,
			CurrentPicker:    match.CurrentPicker,
			Deadline:         match.PickDeadline.Format(time.RFC3339),
		}
		if err := h.templates.ExecuteTemplate(&buf, "draft", data); err != nil {
			log.Printf("Failed to render initial draft: %v", err)
			return ""
		}

	case coordinator.MatchStateWaitingForBot:
		data := struct {
			MatchID  string
			Message  string
			Deadline string
		}{
			MatchID:  match.ID,
			Message:  "Waiting for Dota 2 lobby...",
			Deadline: match.LobbyDeadline.Format(time.RFC3339),
		}
		if err := h.templates.ExecuteTemplate(&buf, "waiting-for-bot", data); err != nil {
			log.Printf("Failed to render initial waiting state: %v", err)
			return ""
		}

	case coordinator.MatchStateInProgress:
		data := struct {
			MatchID     string
			DotaMatchID uint64
		}{
			MatchID:     match.ID,
			DotaMatchID: match.DotaMatchID,
		}
		if err := h.templates.ExecuteTemplate(&buf, "match-in-progress", data); err != nil {
			log.Printf("Failed to render initial in-progress state: %v", err)
			return ""
		}
	}

	return buf.String()
}

func (h *SSEHub) renderActiveMatches() string {
	_, matches, _, _ := h.coordinator.GetState()

	matchList := make([]*coordinator.Match, 0, len(matches))
	for _, m := range matches {
		matchList = append(matchList, m)
	}

	var buf bytes.Buffer
	data := struct {
		Matches []*coordinator.Match
	}{Matches: matchList}

	if err := h.templates.ExecuteTemplate(&buf, "active-matches", data); err != nil {
		log.Printf("Failed to render active matches: %v", err)
		return ""
	}

	return buf.String()
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

func (h *SSEHub) SendToUser(userID string, html string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		if client.UserID == userID {
			select {
			case client.Channel <- html:
			default:
			}
			return
		}
	}
}

func isUserInMatch(userID string, players []coordinator.Player) bool {
	return isUserInPlayers(userID, players)
}

func isUserInPlayers(userID string, players []coordinator.Player) bool {
	for _, p := range players {
		if p.SteamID == userID {
			return true
		}
	}
	return false
}
