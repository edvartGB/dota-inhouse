package web

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/edvart/dota-inhouse/internal/auth"
	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/edvart/dota-inhouse/internal/store"
	"github.com/go-chi/chi/v5"
)

type brokenMatchData struct {
	store.MatchWithPlayers
	PlayerCount   int
	MissingFields []string
}

// handleAdminPage renders the admin dashboard.
func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	queue, matches, lobbySettings, queueOpen := s.coordinator.GetState()

	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		log.Printf("Failed to list users: %v", err)
	}

	brokenMatches, err := s.loadBrokenMatches(r.Context(), matches)
	if err != nil {
		log.Printf("Failed to list broken matches: %v", err)
	}

	data := map[string]interface{}{
		"User":          user,
		"QueueCount":    len(queue),
		"MatchCount":    len(matches),
		"BrokenCount":   len(brokenMatches),
		"UsersCount":    len(users),
		"CurrentMode":   coordinator.ValidGameModes[lobbySettings.GameMode],
		"CurrentModeID": lobbySettings.GameMode,
		"QueueOpen":     queueOpen,
	}

	if err := s.templates.ExecuteTemplate(w, "admin.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleAdminQueuePage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	queue, _, _, _ := s.coordinator.GetState()

	data := map[string]interface{}{
		"User":  user,
		"Queue": queue,
	}

	if err := s.templates.ExecuteTemplate(w, "admin-queue.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleAdminMatchesPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	_, matches, _, _ := s.coordinator.GetState()

	data := map[string]interface{}{
		"User":    user,
		"Matches": matches,
	}

	if err := s.templates.ExecuteTemplate(w, "admin-matches.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleAdminCaptainsPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		log.Printf("Failed to list users: %v", err)
	}

	sortBy, sortDir := sanitizeAdminCaptainSort(r.URL.Query().Get("sort"), r.URL.Query().Get("dir"))
	sortAdminCaptainUsers(users, sortBy, sortDir)

	data := map[string]interface{}{
		"User":    user,
		"Users":   users,
		"SortBy":  sortBy,
		"SortDir": sortDir,
	}

	if err := s.templates.ExecuteTemplate(w, "admin-captains.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func sanitizeAdminCaptainSort(sortBy, sortDir string) (string, string) {
	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	sortDir = strings.ToLower(strings.TrimSpace(sortDir))

	switch sortBy {
	case "name", "steamid", "priority":
	default:
		sortBy = "name"
	}

	if sortDir != "asc" && sortDir != "desc" {
		sortDir = "asc"
	}

	return sortBy, sortDir
}

func sortAdminCaptainUsers(users []store.User, sortBy, sortDir string) {
	sort.SliceStable(users, func(i, j int) bool {
		a := users[i]
		b := users[j]

		var cmp int
		switch sortBy {
		case "steamid":
			cmp = strings.Compare(strings.ToLower(a.SteamID), strings.ToLower(b.SteamID))
		case "priority":
			cmp = compareInt(a.CaptainPriority, b.CaptainPriority)
		default:
			cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}

		if cmp == 0 {
			cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}
		if cmp == 0 {
			cmp = strings.Compare(strings.ToLower(a.SteamID), strings.ToLower(b.SteamID))
		}

		if sortDir == "desc" {
			return cmp > 0
		}
		return cmp < 0
	})
}

func (s *Server) handleAdminSettingsPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	_, _, lobbySettings, queueOpen := s.coordinator.GetState()

	data := map[string]interface{}{
		"User":           user,
		"LobbySettings":  lobbySettings,
		"ValidGameModes": coordinator.ValidGameModes,
		"QueueOpen":      queueOpen,
	}

	if err := s.templates.ExecuteTemplate(w, "admin-settings.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleAdminBrokenMatchesPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	_, matches, _, _ := s.coordinator.GetState()

	brokenMatches, err := s.loadBrokenMatches(r.Context(), matches)
	if err != nil {
		log.Printf("Failed to list broken matches: %v", err)
	}

	data := map[string]interface{}{
		"User":          user,
		"BrokenMatches": brokenMatches,
	}

	if err := s.templates.ExecuteTemplate(w, "admin-broken-matches.html", data); err != nil {
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

func (s *Server) handleAdminSetQueueStatus(w http.ResponseWriter, r *http.Request) {
	status := chi.URLParam(r, "status")
	var open bool
	switch status {
	case "open":
		open = true
	case "close":
		open = false
	default:
		http.Error(w, "status must be 'open' or 'close'", http.StatusBadRequest)
		return
	}

	resp := make(chan error, 1)
	s.coordinator.Send(coordinator.AdminSetQueueOpen{
		Open:     open,
		Response: resp,
	})

	if err := waitForResponse(resp); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Admin set queue status: open=%v", open)
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

	// Persist immediately so a restart before match recorder flush won't leave a broken row.
	if err := s.finalizeStoredMatch(r.Context(), matchID, winner, "", "", ""); err != nil {
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

	winner, err := parseWinnerInput(chi.URLParam(r, "winner"), true)
	if err != nil {
		http.Error(w, "winner must be 'radiant', 'dire', or 'none'", http.StatusBadRequest)
		return
	}

	match, err := s.store.GetMatch(r.Context(), matchID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if match == nil {
		http.Error(w, "match not found", http.StatusBadRequest)
		return
	}

	match.Winner = winner
	if err := s.store.UpdateMatch(r.Context(), match); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if winner == nil {
		log.Printf("Admin cleared history match %s result", matchID[:8])
	} else {
		log.Printf("Admin set history match %s result: %s wins", matchID[:8], *winner)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminRepairHistoryMatch marks an incomplete match as completed and fills missing metadata.
func (s *Server) handleAdminRepairHistoryMatch(w http.ResponseWriter, r *http.Request) {
	matchID := chi.URLParam(r, "matchID")
	if matchID == "" {
		http.Error(w, "match ID required", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}

	winner := strings.TrimSpace(r.FormValue("winner"))
	endedAt := strings.TrimSpace(r.FormValue("ended_at"))
	endedDate := strings.TrimSpace(r.FormValue("ended_date"))
	endedTime := strings.TrimSpace(r.FormValue("ended_time"))
	duration := strings.TrimSpace(r.FormValue("duration"))
	dotaMatchID := strings.TrimSpace(r.FormValue("dota_match_id"))
	if endedAt == "" && endedDate != "" {
		if endedTime == "" {
			endedTime = "00:00"
		}
		endedAt = endedDate + "T" + endedTime
	}

	if err := s.finalizeStoredMatch(r.Context(), matchID, winner, endedAt, duration, dotaMatchID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Admin repaired history match %s", matchID[:8])
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

	leagueID := uint32(0)
	leagueIDStr := strings.TrimSpace(r.FormValue("league_id"))
	if leagueIDStr != "" {
		parsedLeagueID, err := strconv.ParseUint(leagueIDStr, 10, 32)
		if err != nil {
			http.Error(w, "league_id must be a positive integer", http.StatusBadRequest)
			return
		}
		leagueID = uint32(parsedLeagueID)
	}

	resp := make(chan error, 1)
	s.coordinator.Send(coordinator.AdminSetLobbySettings{
		Settings: coordinator.LobbySettings{
			GameMode: gameMode,
			LeagueID: leagueID,
		},
		Response: resp,
	})

	if err := waitForResponse(resp); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
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

func (s *Server) handleAdminDiscordPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		log.Printf("Failed to list users for Discord admin page: %v", err)
		http.Error(w, "Failed to load users", http.StatusInternalServerError)
		return
	}

	data := map[string]interface{}{
		"User":            user,
		"Users":           users,
		"DiscordEnabled":  s.discordSvc != nil,
		"Error":           r.URL.Query().Get("error"),
		"SelectedSteamID": r.URL.Query().Get("steam_id"),
		"DefaultMessage":  "Match accept check-in. Please review the queue page.",
	}

	if err := s.templates.ExecuteTemplate(w, "admin-discord.html", data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (s *Server) handleAdminDiscordPing(w http.ResponseWriter, r *http.Request) {
	if s.discordSvc == nil {
		redirectAdminDiscordError(w, r, "", "Discord bot is not configured")
		return
	}

	if err := r.ParseForm(); err != nil {
		redirectAdminDiscordError(w, r, "", "Invalid form data")
		return
	}

	steamID := strings.TrimSpace(r.FormValue("steam_id"))
	if steamID == "" {
		redirectAdminDiscordError(w, r, "", "Select a user")
		return
	}

	user, err := s.store.GetUser(r.Context(), steamID)
	if err != nil {
		redirectAdminDiscordError(w, r, steamID, "Failed to load user")
		return
	}
	if user == nil {
		redirectAdminDiscordError(w, r, steamID, "User not found")
		return
	}
	if strings.TrimSpace(user.DiscordUserID) == "" {
		redirectAdminDiscordError(w, r, steamID, "User has no Discord user ID linked")
		return
	}

	message := strings.TrimSpace(r.FormValue("message"))
	if message == "" {
		redirectAdminDiscordError(w, r, steamID, "Message cannot be empty")
		return
	}
	if len(message) > 300 {
		redirectAdminDiscordError(w, r, steamID, "Message must be 300 characters or less")
		return
	}

	content := fmt.Sprintf("Manual admin ping for %s:\n<@%s> %s", user.Name, user.DiscordUserID, message)
	if err := s.discordSvc.SendMessage(r.Context(), content, []string{user.DiscordUserID}); err != nil {
		log.Printf("Admin Discord ping failed for %s: %v", user.SteamID, err)
		redirectAdminDiscordError(w, r, steamID, "Failed to send Discord message")
		return
	}

	log.Printf("Admin sent manual Discord ping to %s (%s)", user.Name, user.SteamID)
	http.Redirect(w, r, "/admin/discord?steam_id="+url.QueryEscape(steamID), http.StatusSeeOther)
}

func redirectAdminDiscordError(w http.ResponseWriter, r *http.Request, steamID, message string) {
	target := "/admin/discord?error=" + url.QueryEscape(message)
	if steamID != "" {
		target += "&steam_id=" + url.QueryEscape(steamID)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
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
	queue, matches, lobbySettings, queueOpen := s.coordinator.GetState()

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
		"queueOpen":     queueOpen,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func missingMatchFields(m store.MatchWithPlayers, playerCount int) []string {
	missing := make([]string, 0, 5)
	if m.Winner == nil {
		missing = append(missing, "winner")
	}
	if m.EndedAt == nil {
		missing = append(missing, "ended_at")
	}
	if m.Duration == nil {
		missing = append(missing, "duration")
	}
	if m.DotaMatchID == 0 {
		missing = append(missing, "dota_match_id")
	}
	if playerCount < 10 {
		missing = append(missing, "players")
	}
	return missing
}

func (s *Server) loadBrokenMatches(ctx context.Context, activeMatches map[string]*coordinator.Match) ([]brokenMatchData, error) {
	incompleteMatches, err := s.store.ListIncompleteMatchesWithPlayers(ctx, 100)
	if err != nil {
		return nil, err
	}

	activeMatchIDs := make(map[string]struct{}, len(activeMatches))
	for id := range activeMatches {
		activeMatchIDs[id] = struct{}{}
	}

	brokenMatches := make([]brokenMatchData, 0, len(incompleteMatches))
	for _, m := range incompleteMatches {
		if _, isActive := activeMatchIDs[m.ID]; isActive {
			continue
		}
		playerCount := len(m.Radiant) + len(m.Dire)
		brokenMatches = append(brokenMatches, brokenMatchData{
			MatchWithPlayers: m,
			PlayerCount:      playerCount,
			MissingFields:    missingMatchFields(m, playerCount),
		})
	}

	return brokenMatches, nil
}

func (s *Server) finalizeStoredMatch(ctx context.Context, matchID, winner, endedAtInput, durationInput, dotaMatchIDInput string) error {
	match, err := s.store.GetMatch(ctx, matchID)
	if err != nil {
		return err
	}
	if match == nil {
		return errBadRequest("match not found")
	}

	winnerValue, err := parseWinnerInput(winner, true)
	if err != nil {
		return errBadRequest("winner must be 'radiant', 'dire', or 'none'")
	}

	endedAt, err := parseEndedAtInput(endedAtInput)
	if err != nil {
		return errBadRequest("ended_at must use format YYYY-MM-DDTHH:MM (or provide ended_date + ended_time)")
	}
	if endedAt == nil {
		now := time.Now()
		endedAt = &now
	}

	duration, err := parseDurationInput(durationInput)
	if err != nil {
		return errBadRequest("duration must be a non-negative integer (seconds)")
	}

	dotaMatchID, err := parseDotaMatchIDInput(dotaMatchIDInput)
	if err != nil {
		return errBadRequest("dota_match_id must be a positive integer")
	}

	match.State = "completed"
	match.Winner = winnerValue
	match.EndedAt = endedAt
	if duration != nil {
		match.Duration = duration
	}
	if dotaMatchID != nil {
		match.DotaMatchID = *dotaMatchID
	}

	return s.store.UpdateMatch(ctx, match)
}

func parseWinnerInput(raw string, allowNone bool) (*string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "radiant", "dire":
		winner := strings.ToLower(strings.TrimSpace(raw))
		return &winner, nil
	case "", "none", "null":
		if allowNone {
			return nil, nil
		}
	}
	return nil, errBadRequest("invalid winner")
}

func parseEndedAtInput(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}

	if t, err := time.ParseInLocation("2006-01-02T15:04", raw, time.Local); err == nil {
		return &t, nil
	}

	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t, nil
	}

	return nil, errBadRequest("invalid ended_at")
}

func parseDurationInput(raw string) (*int, error) {
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return nil, errBadRequest("invalid duration")
	}
	return &v, nil
}

func parseDotaMatchIDInput(raw string) (*uint64, error) {
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || v == 0 {
		return nil, errBadRequest("invalid dota match id")
	}
	return &v, nil
}

func errBadRequest(msg string) error {
	return &badRequestError{msg: msg}
}

type badRequestError struct {
	msg string
}

func (e *badRequestError) Error() string {
	return e.msg
}
