package push

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/edvart/dota-inhouse/internal/store"
)

type Service struct {
	store         store.Store
	vapidPublic   string
	vapidPrivate  string
	vapidSubject  string
}

type Config struct {
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string // mailto:your-email@example.com
}

func NewService(st store.Store, cfg Config) *Service {
	return &Service{
		store:        st,
		vapidPublic:  cfg.VAPIDPublicKey,
		vapidPrivate: cfg.VAPIDPrivateKey,
		vapidSubject: cfg.VAPIDSubject,
	}
}

type NotificationPayload struct {
	Title string                 `json:"title"`
	Body  string                 `json:"body"`
	Icon  string                 `json:"icon,omitempty"`
	Badge string                 `json:"badge,omitempty"`
	Data  map[string]interface{} `json:"data,omitempty"`
	Tag   string                 `json:"tag,omitempty"`
}

// SendToUser sends a push notification to all subscriptions for a specific user
func (s *Service) SendToUser(ctx context.Context, steamID string, payload NotificationPayload) error {
	subs, err := s.store.GetPushSubscriptions(ctx, steamID)
	if err != nil {
		return fmt.Errorf("failed to get subscriptions: %w", err)
	}

	if len(subs) == 0 {
		log.Printf("No push subscriptions found for user %s", steamID)
		return nil
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	var lastErr error
	successCount := 0

	for _, sub := range subs {
		subscription := &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys: webpush.Keys{
				P256dh: sub.P256dh,
				Auth:   sub.Auth,
			},
		}

		resp, err := webpush.SendNotification(payloadBytes, subscription, &webpush.Options{
			Subscriber:      s.vapidSubject,
			VAPIDPublicKey:  s.vapidPublic,
			VAPIDPrivateKey: s.vapidPrivate,
			TTL:             60, // Time to live in seconds
		})

		if err != nil {
			log.Printf("Failed to send push to %s: %v", sub.Endpoint, err)
			lastErr = err
			continue
		}
		defer resp.Body.Close()

		// If subscription is gone (410) or invalid (404), delete it
		if resp.StatusCode == 410 || resp.StatusCode == 404 {
			log.Printf("Subscription expired/invalid, removing: %s", sub.Endpoint)
			if err := s.store.DeletePushSubscription(ctx, sub.Endpoint); err != nil {
				log.Printf("Failed to delete subscription: %v", err)
			}
		} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			log.Printf("Push notification failed with status %d for %s", resp.StatusCode, sub.Endpoint)
			lastErr = fmt.Errorf("push failed with status %d", resp.StatusCode)
		} else {
			successCount++
			log.Printf("Push notification sent successfully to %s", steamID)
		}
	}

	if successCount > 0 {
		return nil // At least one succeeded
	}

	if lastErr != nil {
		return lastErr
	}

	return fmt.Errorf("all push notifications failed")
}

// SendToMultipleUsers sends a push notification to multiple users
func (s *Service) SendToMultipleUsers(ctx context.Context, steamIDs []string, payload NotificationPayload) {
	for _, steamID := range steamIDs {
		// Send in background, don't block
		go func(id string) {
			if err := s.SendToUser(ctx, id, payload); err != nil {
				log.Printf("Failed to send push to user %s: %v", id, err)
			}
		}(steamID)
	}
}

// GetPublicKey returns the VAPID public key for frontend use
func (s *Service) GetPublicKey() string {
	return s.vapidPublic
}
