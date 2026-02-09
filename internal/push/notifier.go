package push

import (
	"context"
	"log"

	"github.com/edvart/dota-inhouse/internal/coordinator"
)

// Notifier listens to coordinator events and sends push notifications
type Notifier struct {
	service *Service
}

func NewNotifier(service *Service) *Notifier {
	return &Notifier{
		service: service,
	}
}

// Run starts listening to coordinator events
func (n *Notifier) Run(ctx context.Context, events <-chan coordinator.Event) {
	log.Println("Push notifier started")

	for {
		select {
		case <-ctx.Done():
			log.Println("Push notifier stopped")
			return

		case event := <-events:
			n.handleEvent(ctx, event)
		}
	}
}

func (n *Notifier) handleEvent(ctx context.Context, event coordinator.Event) {
	switch e := event.(type) {
	case coordinator.MatchAcceptStarted:
		n.handleMatchAcceptStarted(ctx, e)
	case coordinator.MatchCancelled:
		n.handleMatchCancelled(ctx, e)
	case coordinator.DraftStarted:
		n.handleDraftStarted(ctx, e)
	// Add more event types as needed
	}
}

func (n *Notifier) handleMatchAcceptStarted(ctx context.Context, event coordinator.MatchAcceptStarted) {
	log.Printf("Sending push notification for match %s to %d players", event.MatchID, len(event.Players))

	payload := NotificationPayload{
		Title: "DNDL Match Found! 🎮",
		Body:  "Click to accept your match.",
		Icon:  "/static/favicon.ico",
		Badge: "/static/favicon.ico",
		Tag:   "match-found",
		Data: map[string]interface{}{
			"matchID": event.MatchID,
			"url":     "/",
		},
	}

	steamIDs := make([]string, len(event.Players))
	for i, p := range event.Players {
		steamIDs[i] = p.SteamID
	}

	n.service.SendToMultipleUsers(ctx, steamIDs, payload)
}

func (n *Notifier) handleMatchCancelled(ctx context.Context, event coordinator.MatchCancelled) {
	log.Printf("Match %s cancelled, could send notification if needed", event.MatchID)
	// Optional: send cancellation notification
}

func (n *Notifier) handleDraftStarted(ctx context.Context, event coordinator.DraftStarted) {
	log.Printf("Draft started for match %s", event.MatchID)

	// Notify captains
	payload := NotificationPayload{
		Title: "It's Your Turn! 🎯",
		Body:  "Time to pick your team!",
		Icon:  "/static/favicon.ico",
		Badge: "/static/favicon.ico",
		Tag:   "draft-started",
		Data: map[string]interface{}{
			"matchID": event.MatchID,
			"url":     "/",
		},
	}

	// Send to both captains
	captainIDs := []string{event.Captains[0].SteamID, event.Captains[1].SteamID}
	n.service.SendToMultipleUsers(ctx, captainIDs, payload)
}
