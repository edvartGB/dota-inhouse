package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/edvart/dota-inhouse/internal/store"
)

const discordAPIBaseURL = "https://discord.com/api/v10"

type Service struct {
	botToken  string
	channelID string
	client    *http.Client
}

type Config struct {
	BotToken  string
	ChannelID string
}

func NewService(cfg Config) *Service {
	return &Service{
		botToken:  cfg.BotToken,
		channelID: cfg.ChannelID,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type Notifier struct {
	service *Service
	store   store.Store

	mu          sync.Mutex
	notifiedIDs map[string]struct{}
}

func NewNotifier(service *Service, st store.Store) *Notifier {
	return &Notifier{
		service:     service,
		store:       st,
		notifiedIDs: make(map[string]struct{}),
	}
}

func (n *Notifier) Run(ctx context.Context, events <-chan coordinator.Event) {
	log.Println("Discord notifier started")

	for {
		select {
		case <-ctx.Done():
			log.Println("Discord notifier stopped")
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
	case coordinator.MatchCompleted:
		n.forgetMatch(e.MatchID)
	case coordinator.MatchCancelled:
		n.forgetMatch(e.MatchID)
	}
}

func (n *Notifier) handleMatchAcceptStarted(ctx context.Context, event coordinator.MatchAcceptStarted) {
	if !n.markNotified(event.MatchID) {
		return
	}

	var mentionTokens []string
	var mentionUserIDs []string
	var nonLinkedPlayers []string

	for _, p := range event.Players {
		user, err := n.store.GetUser(ctx, p.SteamID)
		if err != nil {
			log.Printf("Discord notifier: failed to load user %s: %v", p.SteamID, err)
			nonLinkedPlayers = append(nonLinkedPlayers, p.Name)
			continue
		}
		if user == nil {
			nonLinkedPlayers = append(nonLinkedPlayers, p.Name)
			continue
		}
		if user.DiscordUserID == "" {
			if user.DiscordUsername != "" {
				nonLinkedPlayers = append(nonLinkedPlayers, fmt.Sprintf("%s (@%s)", p.Name, user.DiscordUsername))
			} else {
				nonLinkedPlayers = append(nonLinkedPlayers, p.Name)
			}
			continue
		}
		mentionTokens = append(mentionTokens, fmt.Sprintf("<@%s>", user.DiscordUserID))
		mentionUserIDs = append(mentionUserIDs, user.DiscordUserID)
	}

	content := buildMatchAcceptMessage(event, mentionTokens, nonLinkedPlayers)
	if err := n.service.SendMessage(ctx, content, mentionUserIDs); err != nil {
		log.Printf("Discord notifier: failed to send match-accept notification for %s: %v", event.MatchID, err)
		return
	}

	log.Printf("Discord notifier: sent match-accept notification for %s (%d linked, %d missing)",
		event.MatchID, len(mentionTokens), len(nonLinkedPlayers))
}

func (n *Notifier) markNotified(matchID string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	if _, ok := n.notifiedIDs[matchID]; ok {
		return false
	}
	n.notifiedIDs[matchID] = struct{}{}
	return true
}

func (n *Notifier) forgetMatch(matchID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.notifiedIDs, matchID)
}

func buildMatchAcceptMessage(event coordinator.MatchAcceptStarted, mentionTokens []string, nonLinkedPlayers []string) string {
	shortID := event.MatchID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	secondsLeft := int(time.Until(event.Deadline).Seconds())
	if secondsLeft < 0 {
		secondsLeft = 0
	}

	lines := []string{
		fmt.Sprintf("Match found (`%s`) - accept within %ds.", shortID, secondsLeft),
	}

	if len(mentionTokens) > 0 {
		lines = append(lines, "Linked players: "+strings.Join(mentionTokens, " "))
	}

	if len(nonLinkedPlayers) > 0 {
		lines = append(lines, "Missing Discord link: "+strings.Join(nonLinkedPlayers, ", "))
	}

	return strings.Join(lines, "\n")
}

func (s *Service) SendMessage(ctx context.Context, content string, mentionedUserIDs []string) error {
	payload := struct {
		Content         string `json:"content"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
			Users []string `json:"users,omitempty"`
		} `json:"allowed_mentions"`
	}{
		Content: content,
	}

	// Disable broad mentions and allow only explicit user IDs.
	payload.AllowedMentions.Parse = []string{}
	if len(mentionedUserIDs) > 0 {
		payload.AllowedMentions.Users = dedupe(mentionedUserIDs)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal discord payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/channels/%s/messages", discordAPIBaseURL, s.channelID),
		bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create discord request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+s.botToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send discord request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("discord API status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
}

func dedupe(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}
