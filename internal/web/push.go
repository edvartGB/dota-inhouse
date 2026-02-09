package web

import (
	"encoding/json"
	"net/http"

	"github.com/edvart/dota-inhouse/internal/auth"
	"github.com/edvart/dota-inhouse/internal/push"
	"github.com/edvart/dota-inhouse/internal/store"
)

type PushSubscriptionRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// handleSubscribePush handles push subscription from frontend
func (s *Server) handleSubscribePush(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req PushSubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	sub := &store.PushSubscription{
		SteamID:  user.SteamID,
		Endpoint: req.Endpoint,
		P256dh:   req.Keys.P256dh,
		Auth:     req.Keys.Auth,
	}

	if err := s.store.SavePushSubscription(r.Context(), sub); err != nil {
		http.Error(w, "Failed to save subscription", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// handleUnsubscribePush handles push unsubscription
func (s *Server) handleUnsubscribePush(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if err := s.store.DeletePushSubscription(r.Context(), req.Endpoint); err != nil {
		http.Error(w, "Failed to delete subscription", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// handleGetVAPIDPublicKey returns the VAPID public key for frontend
func (s *Server) handleGetVAPIDPublicKey(w http.ResponseWriter, r *http.Request) {
	if s.pushService == nil {
		http.Error(w, "Push notifications not configured", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"publicKey": s.pushService.GetPublicKey(),
	})
}

// handleTestPush sends a test push notification to the current user
func (s *Server) handleTestPush(w http.ResponseWriter, r *http.Request) {
	if s.pushService == nil {
		http.Error(w, "Push notifications not configured", http.StatusServiceUnavailable)
		return
	}

	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	payload := push.NotificationPayload{
		Title: "Test Notification 🧪",
		Body:  "If you see this, push notifications are working!",
		Icon:  "/static/favicon.ico",
		Badge: "/static/favicon.ico",
		Tag:   "test-notification",
		Data: map[string]interface{}{
			"url": "/",
		},
	}

	if err := s.pushService.SendToUser(r.Context(), user.SteamID, payload); err != nil {
		http.Error(w, "Failed to send test notification", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "Test notification sent"})
}
