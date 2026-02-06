package web

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/edvart/dota-inhouse/internal/coordinator"
)

// SSEClient represents a connected SSE client.
type SSEClient struct {
	ID      string
	UserID  string
	Channel chan string
}

// SSEHub manages SSE connections and broadcasts events.
type SSEHub struct {
	clients     map[*SSEClient]bool
	mu          sync.RWMutex
	templates   *template.Template
	coordinator *coordinator.Coordinator
	devMode     bool
}

// NewSSEHub creates a new SSE hub.
func NewSSEHub(templates *template.Template, coord *coordinator.Coordinator, devMode bool) *SSEHub {
	return &SSEHub{
		clients:     make(map[*SSEClient]bool),
		templates:   templates,
		coordinator: coord,
		devMode:     devMode,
	}
}

// Run starts the SSE hub, processing events from the coordinator.
func (h *SSEHub) Run(events <-chan coordinator.Event) {
	log.Println("SSE hub started")
	for event := range events {
		h.broadcast(event)
	}
}

func (h *SSEHub) broadcast(event coordinator.Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Check if this is a match-related event that should update the matches panel for everyone
	var matchesHTML string
	if h.isMatchEvent(event) {
		matchesHTML = h.renderActiveMatches()
	}

	for client := range h.clients {
		html := h.renderEventForUser(event, client.UserID)

		// For match events, always send the matches panel update even if user isn't in the match
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

// isMatchEvent returns true if the event affects match state.
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
	case coordinator.QueueUpdated:
		// Check if user is in queue for personalized button state
		inQueue := false
		for _, p := range e.Queue {
			if p.SteamID == userID {
				inQueue = true
				break
			}
		}
		// Check if user is in a match
		inMatch := h.coordinator.GetPlayerMatch(userID) != nil
		data := struct {
			Queue   []coordinator.Player
			InQueue bool
			InMatch bool
		}{Queue: e.Queue, InQueue: inQueue, InMatch: inMatch}
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
			MatchID  string
			Players  []coordinator.Player
			Deadline string
			Count    int
			Total    int
		}{
			MatchID:  e.MatchID,
			Players:  e.Players,
			Deadline: e.Deadline.Format("2006-01-02T15:04:05Z"),
			Count:    0,
			Total:    coordinator.MaxPlayers,
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
		data := struct {
			MatchID  string
			Accepted map[string]bool
			Count    int
			Total    int
		}{
			MatchID:  e.MatchID,
			Accepted: e.Accepted,
			Count:    len(e.Accepted),
			Total:    coordinator.MaxPlayers,
		}
		if err := h.templates.ExecuteTemplate(&buf, "accept-status", data); err != nil {
			log.Printf("Failed to render accept status: %v", err)
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
			DevMode:          h.devMode,
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
			DevMode:          h.devMode,
		}
		if err := h.templates.ExecuteTemplate(&buf, "draft", data); err != nil {
			log.Printf("Failed to render draft: %v", err)
			return ""
		}

	case coordinator.MatchCancelled:
		// Only send to users who were in this match (check via coordinator - they're back in queue now)
		// Since players are returned to queue, we check if user's match is nil and they got this event
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
		// Also render queue panel for users returned to queue
		if isUserInPlayers(userID, e.ReturnedToQueue) {
			queue, _ := h.coordinator.GetState()
			inQueue := false
			for _, p := range queue {
				if p.SteamID == userID {
					inQueue = true
					break
				}
			}
			queueData := struct {
				Queue   []coordinator.Player
				InQueue bool
				InMatch bool
			}{Queue: queue, InQueue: inQueue, InMatch: false}
			if err := h.templates.ExecuteTemplate(&buf, "queue-sse", queueData); err != nil {
				log.Printf("Failed to render queue after draft cancelled: %v", err)
			}
		}

	case coordinator.LobbyCancelled:
		// Send to users who were in this match (both failed and returned)
		wasInMatch := isUserInPlayers(userID, e.ReturnedToQueue) || isUserInPlayers(userID, e.FailedPlayers)
		if !wasInMatch {
			return ""
		}
		if err := h.templates.ExecuteTemplate(&buf, "lobby-cancelled", e); err != nil {
			log.Printf("Failed to render lobby cancelled: %v", err)
			return ""
		}
		// Also render queue panel for users returned to queue
		if isUserInPlayers(userID, e.ReturnedToQueue) {
			queue, _ := h.coordinator.GetState()
			inQueue := false
			for _, p := range queue {
				if p.SteamID == userID {
					inQueue = true
					break
				}
			}
			queueData := struct {
				Queue   []coordinator.Player
				InQueue bool
				InMatch bool
			}{Queue: queue, InQueue: inQueue, InMatch: false}
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
			MatchID string
			Message string
		}{
			MatchID: e.MatchID,
			Message: "Waiting for Dota 2 lobby...",
		}
		if err := h.templates.ExecuteTemplate(&buf, "waiting-for-bot", data); err != nil {
			log.Printf("Failed to render waiting: %v", err)
			return ""
		}

	case coordinator.MatchCompleted:
		// Check if user was in this match
		wasInMatch := isUserInPlayers(userID, e.Players)
		if !wasInMatch {
			return "" // User wasn't in this match, no update needed
		}
		// Render match completed notification
		if err := h.templates.ExecuteTemplate(&buf, "match-completed", e); err != nil {
			log.Printf("Failed to render match completed: %v", err)
			return ""
		}
		// Also render the queue panel so user can join queue again
		queue, _ := h.coordinator.GetState()
		inQueue := false
		for _, p := range queue {
			if p.SteamID == userID {
				inQueue = true
				break
			}
		}
		queueData := struct {
			Queue   []coordinator.Player
			InQueue bool
			InMatch bool
		}{Queue: queue, InQueue: inQueue, InMatch: false}
		if err := h.templates.ExecuteTemplate(&buf, "queue-sse", queueData); err != nil {
			log.Printf("Failed to render queue after match completed: %v", err)
		}

	default:
		return ""
	}

	return buf.String()
}

// DraftData holds data for rendering the draft UI.
type DraftData struct {
	MatchID          string
	Captains         [2]coordinator.Player
	AvailablePlayers []coordinator.Player
	Radiant          []coordinator.Player
	Dire             []coordinator.Player
	CurrentPicker    int
	DevMode          bool
}

// renderInitialState renders the current state for a newly connected client.
func (h *SSEHub) renderInitialState(userID string) string {
	queue, matches := h.coordinator.GetState()

	// Check if user is in queue
	inQueue := false
	for _, p := range queue {
		if p.SteamID == userID {
			inQueue = true
			break
		}
	}

	// Check if user is in a match
	inMatch := h.coordinator.GetPlayerMatch(userID) != nil

	// Convert matches map to slice
	matchList := make([]*coordinator.Match, 0, len(matches))
	for _, m := range matches {
		matchList = append(matchList, m)
	}

	var buf bytes.Buffer

	// Render queue
	queueData := struct {
		Queue   []coordinator.Player
		InQueue bool
		InMatch bool
	}{Queue: queue, InQueue: inQueue, InMatch: inMatch}
	if err := h.templates.ExecuteTemplate(&buf, "queue-sse", queueData); err != nil {
		log.Printf("Failed to render initial queue: %v", err)
		return ""
	}

	// Render active matches
	matchesData := struct {
		Matches []*coordinator.Match
	}{Matches: matchList}
	if err := h.templates.ExecuteTemplate(&buf, "active-matches", matchesData); err != nil {
		log.Printf("Failed to render initial matches: %v", err)
		return ""
	}

	return buf.String()
}

// renderActiveMatches renders the active-matches panel.
func (h *SSEHub) renderActiveMatches() string {
	_, matches := h.coordinator.GetState()

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

// HandleConnection handles a new SSE connection.
func (h *SSEHub) HandleConnection(w http.ResponseWriter, r *http.Request, userID string) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Create client
	client := &SSEClient{
		ID:      fmt.Sprintf("%p", r),
		UserID:  userID,
		Channel: make(chan string, 10),
	}

	// Register client
	h.mu.Lock()
	h.clients[client] = true
	h.mu.Unlock()

	log.Printf("SSE client connected: %s (user: %s)", client.ID, userID)

	// Ensure cleanup on disconnect
	defer func() {
		h.mu.Lock()
		delete(h.clients, client)
		h.mu.Unlock()
		close(client.Channel)
		log.Printf("SSE client disconnected: %s", client.ID)
	}()

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Send initial keepalive
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	// Send initial state sync
	if initialHTML := h.renderInitialState(userID); initialHTML != "" {
		lines := strings.Split(initialHTML, "\n")
		for _, line := range lines {
			fmt.Fprintf(w, "data: %s\n", line)
		}
		fmt.Fprintf(w, "\n")
		flusher.Flush()
	}

	// Listen for events or disconnect
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-client.Channel:
			if !ok {
				return
			}
			// Send SSE message - each line must be prefixed with "data: "
			lines := strings.Split(msg, "\n")
			for _, line := range lines {
				fmt.Fprintf(w, "data: %s\n", line)
			}
			fmt.Fprintf(w, "\n") // Empty line marks end of message
			flusher.Flush()
		}
	}
}

// SendToUser sends a message to a specific user.
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

// isUserInMatch checks if a user is in the given player list.
func isUserInMatch(userID string, players []coordinator.Player) bool {
	return isUserInPlayers(userID, players)
}

// isUserInPlayers checks if a user is in the given player list.
func isUserInPlayers(userID string, players []coordinator.Player) bool {
	for _, p := range players {
		if p.SteamID == userID {
			return true
		}
	}
	return false
}
