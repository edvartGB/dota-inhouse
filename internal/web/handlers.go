package web

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/edvart/dota-inhouse/internal/auth"
	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/edvart/dota-inhouse/internal/store"
	"github.com/go-chi/chi/v5"
)

const handlerTimeout = 10 * time.Second

func banMessage(user *store.User, now time.Time) string {
	if !user.IsBanned(now) {
		return ""
	}
	if user.IsBannedIndefinitely() {
		return "You are banned indefinitely"
	}
	return "You are banned until " + user.BannedUntil.Local().Format("2 Jan 15:04")
}

func (s *Server) runCommand(w http.ResponseWriter, build func(resp chan error) coordinator.Command) bool {
	resp := make(chan error, 1)
	s.coordinator.Send(build(resp))
	var err error
	select {
	case err = <-resp:
	case <-time.After(handlerTimeout):
		err = errors.New("request timed out")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func (s *Server) handleJoinQueue(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if msg := banMessage(user, time.Now()); msg != "" {
		http.Error(w, msg, http.StatusForbidden)
		return
	}

	if !s.runCommand(w, func(resp chan error) coordinator.Command {
		return coordinator.JoinQueue{
			Player: coordinator.Player{
				SteamID:         user.SteamID,
				Name:            user.Name,
				AvatarURL:       user.AvatarURL,
				CaptainPriority: user.CaptainPriority,
			},
			Response: resp,
		}
	}) {
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

	if !s.runCommand(w, func(resp chan error) coordinator.Command {
		return coordinator.LeaveQueue{PlayerID: user.SteamID, Response: resp}
	}) {
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

	if !s.runCommand(w, func(resp chan error) coordinator.Command {
		return coordinator.AcceptMatch{PlayerID: user.SteamID, MatchID: matchID, Response: resp}
	}) {
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleChooseSide(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	matchID := chi.URLParam(r, "matchID")
	side := chi.URLParam(r, "side")
	if matchID == "" || side == "" {
		http.Error(w, "match ID and side required", http.StatusBadRequest)
		return
	}

	if !s.runCommand(w, func(resp chan error) coordinator.Command {
		return coordinator.ChooseSide{CaptainID: user.SteamID, MatchID: matchID, Side: side, Response: resp}
	}) {
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
	if matchID == "" || playerID == "" {
		http.Error(w, "match ID and player ID required", http.StatusBadRequest)
		return
	}

	if !s.runCommand(w, func(resp chan error) coordinator.Command {
		return coordinator.PickPlayer{CaptainID: user.SteamID, PickedID: playerID, MatchID: matchID, Response: resp}
	}) {
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
