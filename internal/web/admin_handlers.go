package web

import (
	"bufio"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/edvart/dota-inhouse/internal/auth"
	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/go-chi/chi/v5"
)

// handleAdminPage renders the admin dashboard.
func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	queue, matches, lobbySettings := s.coordinator.GetState()

	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		log.Printf("Failed to list users: %v", err)
	}

	data := map[string]interface{}{
		"User":           user,
		"Queue":          queue,
		"Matches":        matches,
		"Users":          users,
		"LobbySettings":  lobbySettings,
		"ValidGameModes": coordinator.ValidGameModes,
		"IsAdmin":        true,
		"LogLines":       s.readLogTail(50),
	}

	if err := s.templates.ExecuteTemplate(w, "admin.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleAdminCancelMatch cancels a match.
func (s *Server) handleAdminCancelMatch(w http.ResponseWriter, r *http.Request) {
	matchID := chi.URLParam(r, "matchID")
	if matchID == "" {
		http.Error(w, "match ID required", http.StatusBadRequest)
		return
	}

	returnToQueue := r.URL.Query().Get("return") != "false"

	resp := make(chan error, 1)
	s.coordinator.Send(coordinator.AdminCancelMatch{
		MatchID:       matchID,
		ReturnToQueue: returnToQueue,
		Response:      resp,
	})

	if err := waitForResponse(resp); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Admin cancelled match %s (requeue=%v)", matchID[:8], returnToQueue)
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminSetResult sets the result of a match.
func (s *Server) handleAdminSetResult(w http.ResponseWriter, r *http.Request) {
	matchID := chi.URLParam(r, "matchID")
	if matchID == "" {
		http.Error(w, "match ID required", http.StatusBadRequest)
		return
	}

	winner := chi.URLParam(r, "winner")
	if winner != "radiant" && winner != "dire" {
		http.Error(w, "winner must be 'radiant' or 'dire'", http.StatusBadRequest)
		return
	}

	resp := make(chan error, 1)
	s.coordinator.Send(coordinator.AdminSetMatchResult{
		MatchID:  matchID,
		Winner:   winner,
		Response: resp,
	})

	if err := waitForResponse(resp); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Admin set active match %s result: %s wins", matchID[:8], winner)
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminSetHistoryResult sets the winner of a completed match in the database.
func (s *Server) handleAdminSetHistoryResult(w http.ResponseWriter, r *http.Request) {
	matchID := chi.URLParam(r, "matchID")
	if matchID == "" {
		http.Error(w, "match ID required", http.StatusBadRequest)
		return
	}

	winner := chi.URLParam(r, "winner")
	if winner != "radiant" && winner != "dire" {
		http.Error(w, "winner must be 'radiant' or 'dire'", http.StatusBadRequest)
		return
	}

	if err := s.store.SetMatchWinner(r.Context(), matchID, winner); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Admin set history match %s result: %s wins", matchID[:8], winner)
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminKickPlayer kicks a player from the queue.
func (s *Server) handleAdminKickPlayer(w http.ResponseWriter, r *http.Request) {
	playerID := chi.URLParam(r, "playerID")
	if playerID == "" {
		http.Error(w, "player ID required", http.StatusBadRequest)
		return
	}

	resp := make(chan error, 1)
	s.coordinator.Send(coordinator.AdminKickFromQueue{
		PlayerID: playerID,
		Response: resp,
	})

	if err := waitForResponse(resp); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Admin kicked player %s from queue", playerID)
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminSetCaptainPriority updates a player's captain priority.
func (s *Server) handleAdminSetCaptainPriority(w http.ResponseWriter, r *http.Request) {
	playerID := chi.URLParam(r, "playerID")
	if playerID == "" {
		http.Error(w, "player ID required", http.StatusBadRequest)
		return
	}

	priorityStr := chi.URLParam(r, "priority")
	priority, err := strconv.Atoi(priorityStr)
	if err != nil || priority < 1 || priority > 10 {
		http.Error(w, "priority must be 1-10", http.StatusBadRequest)
		return
	}

	if err := s.store.UpdateCaptainPriority(r.Context(), playerID, priority); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleAdminSetLobbySettings updates lobby settings.
func (s *Server) handleAdminSetLobbySettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}

	gameMode := r.FormValue("game_mode")
	if gameMode == "" {
		http.Error(w, "game_mode required", http.StatusBadRequest)
		return
	}

	resp := make(chan error, 1)
	s.coordinator.Send(coordinator.AdminSetLobbySettings{
		Settings: coordinator.LobbySettings{
			GameMode: gameMode,
		},
		Response: resp,
	})

	if err := waitForResponse(resp); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// handleAdminLogs renders the last N lines of the log file.
func (s *Server) handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	maxLines := 200
	if n, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && n > 0 && n <= 1000 {
		maxLines = n
	}

	lines := s.readLogTail(maxLines)

	data := map[string]interface{}{
		"User":     user,
		"Lines":    lines,
		"MaxLines": maxLines,
	}

	if err := s.templates.ExecuteTemplate(w, "admin-logs.html", data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// readLogTail returns the last n lines from the log file.
func (s *Server) readLogTail(n int) []string {
	if s.logPath == "" {
		return nil
	}
	f, err := os.Open(s.logPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)
	var all []string
	for scanner.Scan() {
		all = append(all, scanner.Text())
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}

// handleAdminState returns the current state as JSON.
func (s *Server) handleAdminState(w http.ResponseWriter, r *http.Request) {
	queue, matches, lobbySettings := s.coordinator.GetState()

	matchList := make([]map[string]interface{}, 0)
	for id, m := range matches {
		matchList = append(matchList, map[string]interface{}{
			"id":          id,
			"state":       m.State.String(),
			"players":     m.Players,
			"radiant":     m.Radiant,
			"dire":        m.Dire,
			"dotaMatchID": m.DotaMatchID,
			"captains":    m.Captains,
			"pickCount":   m.PickCount,
		})
	}

	data := map[string]interface{}{
		"queue":         queue,
		"matches":       matchList,
		"lobbySettings": lobbySettings,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}
