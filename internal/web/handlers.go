package web

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/edvart/dota-inhouse/internal/auth"
	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/go-chi/chi/v5"
)

const handlerTimeout = 10 * time.Second

// waitForResponse waits for a response with a timeout.
func waitForResponse(resp <-chan error) error {
	select {
	case err := <-resp:
		return err
	case <-time.After(handlerTimeout):
		return fmt.Errorf("request timed out")
	}
}

func (s *Server) handleJoinQueue(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	resp := make(chan error, 1)
	s.coordinator.Send(coordinator.JoinQueue{
		Player: coordinator.Player{
			SteamID:         user.SteamID,
			Name:            user.Name,
			AvatarURL:       user.AvatarURL,
			CaptainPriority: user.CaptainPriority,
		},
		Response: resp,
	})

	if err := waitForResponse(resp); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Player %s (%s) joined queue", user.Name, user.SteamID)
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

	if err := waitForResponse(resp); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Player %s (%s) left queue", user.Name, user.SteamID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAcceptMatch(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	matchID := chi.URLParam(r, "matchID")
	if matchID == "" {
		http.Error(w, "match ID required", http.StatusBadRequest)
		return
	}

	resp := make(chan error, 1)
	s.coordinator.Send(coordinator.AcceptMatch{
		PlayerID: user.SteamID,
		MatchID:  matchID,
		Response: resp,
	})

	if err := waitForResponse(resp); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Player %s (%s) accepted match %s", user.Name, user.SteamID, matchID[:8])
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
	if matchID == "" || playerID == "" {
		http.Error(w, "match ID and player ID required", http.StatusBadRequest)
		return
	}

	resp := make(chan error, 1)
	s.coordinator.Send(coordinator.PickPlayer{
		CaptainID: user.SteamID,
		PickedID:  playerID,
		MatchID:   matchID,
		Response:  resp,
	})

	if err := waitForResponse(resp); err != nil {
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
