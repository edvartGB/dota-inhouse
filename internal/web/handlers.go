package web

import (
	"fmt"
	"math/rand"
	"net/http"
	"strings"

	"github.com/edvart/dota-inhouse/internal/auth"
	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleJoinQueue(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	resp := make(chan error, 1)
	s.coordinator.Send(coordinator.JoinQueue{
		Player: coordinator.Player{
			SteamID:   user.SteamID,
			Name:      user.Name,
			AvatarURL: user.AvatarURL,
		},
		Response: resp,
	})

	if err := <-resp; err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLeaveQueue(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	resp := make(chan error, 1)
	s.coordinator.Send(coordinator.LeaveQueue{
		PlayerID: user.SteamID,
		Response: resp,
	})

	if err := <-resp; err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAcceptMatch(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	matchID := chi.URLParam(r, "matchID")

	resp := make(chan error, 1)
	s.coordinator.Send(coordinator.AcceptMatch{
		PlayerID: user.SteamID,
		MatchID:  matchID,
		Response: resp,
	})

	if err := <-resp; err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePickPlayer(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	matchID := chi.URLParam(r, "matchID")
	playerID := chi.URLParam(r, "playerID")

	resp := make(chan error, 1)
	s.coordinator.Send(coordinator.PickPlayer{
		CaptainID: user.SteamID,
		PickedID:  playerID,
		MatchID:   matchID,
		Response:  resp,
	})

	if err := <-resp; err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	user, _ := s.sessions.GetUser(r.Context(), r)

	var userID string
	if user != nil {
		userID = user.SteamID
	}

	s.sse.HandleConnection(w, r, userID)
}

func (s *Server) handleAddFakePlayers(w http.ResponseWriter, r *http.Request) {
	if !s.devMode {
		http.Error(w, "Not available", http.StatusNotFound)
		return
	}

	count := coordinator.MaxPlayers - 1 // Add enough fake players to fill the queue minus one

	for i := 1; i <= count; i++ {
		resp := make(chan error, 1)
		id := rand.Intn(9999999)
		s.coordinator.Send(coordinator.JoinQueue{
			Player: coordinator.Player{
				SteamID:   fmt.Sprintf("fake_%d", id),
				Name:      fmt.Sprintf("Player %d", id),
				AvatarURL: "",
			},
			Response: resp,
		})
		<-resp
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDevAcceptAll(w http.ResponseWriter, r *http.Request) {
	if !s.devMode {
		http.Error(w, "Not available", http.StatusNotFound)
		return
	}

	matchID := r.URL.Query().Get("match")
	if matchID == "" {
		http.Error(w, "match ID required", http.StatusBadRequest)
		return
	}

	// Accept for all fake players
	for i := 1; i <= coordinator.MaxPlayers-1; i++ {
		resp := make(chan error, 1)
		s.coordinator.Send(coordinator.AcceptMatch{
			PlayerID: fmt.Sprintf("fake_%d", i),
			MatchID:  matchID,
			Response: resp,
		})
		<-resp
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDevPick(w http.ResponseWriter, r *http.Request) {
	if !s.devMode {
		http.Error(w, "Not available", http.StatusNotFound)
		return
	}

	matchID := chi.URLParam(r, "matchID")
	playerID := chi.URLParam(r, "playerID")

	// Get the specific match to find the current captain
	_, matches := s.coordinator.GetState()
	match, ok := matches[matchID]
	if !ok || match == nil {
		http.Error(w, "match not found", http.StatusBadRequest)
		return
	}

	captainID := match.Captains[match.CurrentPicker].SteamID

	resp := make(chan error, 1)
	s.coordinator.Send(coordinator.PickPlayer{
		CaptainID: captainID,
		PickedID:  playerID,
		MatchID:   matchID,
		Response:  resp,
	})

	if err := <-resp; err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleDevBotGameStarted simulates the bot reporting that the Dota 2 game has started.
func (s *Server) handleDevBotGameStarted(w http.ResponseWriter, r *http.Request) {
	if !s.devMode {
		http.Error(w, "Not available", http.StatusNotFound)
		return
	}

	matchID := chi.URLParam(r, "matchID")

	// Verify match exists and is in correct state
	_, matches := s.coordinator.GetState()
	match, ok := matches[matchID]
	if !ok || match == nil {
		http.Error(w, "match not found", http.StatusBadRequest)
		return
	}

	s.coordinator.Send(coordinator.BotGameStarted{
		MatchID:     matchID,
		DotaMatchID: 12345678, // Fake Dota match ID
	})

	w.WriteHeader(http.StatusNoContent)
}

// handleDevBotGameEnded simulates the bot reporting that the Dota 2 game has ended.
func (s *Server) handleDevBotGameEnded(w http.ResponseWriter, r *http.Request) {
	if !s.devMode {
		http.Error(w, "Not available", http.StatusNotFound)
		return
	}

	matchID := chi.URLParam(r, "matchID")

	// Verify match exists
	_, matches := s.coordinator.GetState()
	match, ok := matches[matchID]
	if !ok || match == nil {
		http.Error(w, "match not found", http.StatusBadRequest)
		return
	}

	s.coordinator.Send(coordinator.BotGameEnded{
		MatchID:     matchID,
		DotaMatchID: match.DotaMatchID,
	})

	w.WriteHeader(http.StatusNoContent)
}

// handleDevBotLobbyTimeout simulates a lobby timeout where some players failed to join.
// Query param: ?joined=fake_1,fake_2,fake_3 (comma-separated Steam IDs of players who joined correctly)
// If not specified, assumes NO players joined correctly.
func (s *Server) handleDevBotLobbyTimeout(w http.ResponseWriter, r *http.Request) {
	if !s.devMode {
		http.Error(w, "Not available", http.StatusNotFound)
		return
	}

	matchID := chi.URLParam(r, "matchID")

	// Verify match exists and is in waiting for bot state
	_, matches := s.coordinator.GetState()
	match, ok := matches[matchID]
	if !ok || match == nil {
		http.Error(w, "match not found", http.StatusBadRequest)
		return
	}

	// Parse joined players from query param
	var joinedCorrectly []string
	if joined := r.URL.Query().Get("joined"); joined != "" {
		for _, id := range splitAndTrim(joined) {
			if id != "" {
				joinedCorrectly = append(joinedCorrectly, id)
			}
		}
	}

	s.coordinator.Send(coordinator.BotLobbyTimeout{
		MatchID:            matchID,
		PlayersJoinedRight: joinedCorrectly,
	})

	w.WriteHeader(http.StatusNoContent)
}

// splitAndTrim splits a string by comma and trims whitespace from each part.
func splitAndTrim(s string) []string {
	var result []string
	for _, part := range strings.Split(s, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
