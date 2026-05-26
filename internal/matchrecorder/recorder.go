package matchrecorder

import (
	"context"
	"log"
	"time"

	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/edvart/dota-inhouse/internal/dotaapi"
	"github.com/edvart/dota-inhouse/internal/store"
)

type Recorder struct {
	store   store.Store
	dotaAPI *dotaapi.Client
}

func New(s store.Store, dotaAPI *dotaapi.Client) *Recorder {
	return &Recorder{store: s, dotaAPI: dotaAPI}
}

func (r *Recorder) Run(ctx context.Context, events <-chan coordinator.Event) {
	log.Println("Match recorder started")
	for {
		select {
		case <-ctx.Done():
			log.Println("Match recorder shutting down")
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			r.handleEvent(ctx, event)
		}
	}
}

func (r *Recorder) handleEvent(ctx context.Context, event coordinator.Event) {
	switch e := event.(type) {
	case coordinator.MatchStarted:
		r.recordMatchStarted(ctx, e)
	case coordinator.MatchCompleted:
		r.recordMatchCompleted(ctx, e)
	}
}

func (r *Recorder) recordMatchStarted(ctx context.Context, e coordinator.MatchStarted) {
	match := &store.Match{
		ID:          e.MatchID,
		DotaMatchID: e.DotaMatchID,
		State:       "in_progress",
		StartedAt:   time.Now(),
	}
	players := buildMatchPlayers(e.MatchID, e.Radiant, e.Dire, e.Captains)

	if err := r.store.CreateMatchWithPlayers(ctx, match, players); err != nil {
		log.Printf("Match recorder: failed to record match start %s with %d players: %v", e.MatchID[:8], len(players), err)
		return
	}

	log.Printf("Match recorder: recorded match start %s with %d players", e.MatchID[:8], len(players))
}

func (r *Recorder) recordMatchCompleted(ctx context.Context, e coordinator.MatchCompleted) {
	now := time.Now()

	var winner *string
	var duration *int
	if e.DotaMatchID != 0 && r.dotaAPI != nil {
		details, err := r.fetchWithRetry(ctx, e.DotaMatchID)
		if err != nil {
			log.Printf("Match recorder: failed to fetch OpenDota API details for match %d after retries: %v", e.DotaMatchID, err)
			winner = e.Winner
		} else {
			w := details.Winner()
			winner = &w
			duration = &details.Duration
			log.Printf("Match recorder: fetched OpenDota API details - winner: %s, duration: %s", w, details.DurationFormatted())
		}
	} else {
		winner = e.Winner
	}

	existing, err := r.store.GetMatch(ctx, e.MatchID)
	if err != nil {
		log.Printf("Match recorder: failed to get match %s: %v", e.MatchID, err)
		return
	}

	if existing == nil {
		// Match wasn't recorded at start (maybe server restarted), create it now
		match := &store.Match{
			ID:          e.MatchID,
			DotaMatchID: e.DotaMatchID,
			State:       "completed",
			StartedAt:   now, // Unknown actual start time
			EndedAt:     &now,
			Winner:      winner,
			Duration:    duration,
		}
		players := buildMatchPlayers(e.MatchID, e.Radiant, e.Dire, [2]coordinator.Player{})
		if err := r.store.CreateMatchWithPlayers(ctx, match, players); err != nil {
			log.Printf("Match recorder: failed to create completed match %s with %d players: %v", e.MatchID, len(players), err)
			return
		}
	} else {
		existing.State = "completed"
		existing.EndedAt = &now
		existing.Winner = winner
		existing.Duration = duration
		existing.DotaMatchID = e.DotaMatchID
		if err := r.store.UpdateMatch(ctx, existing); err != nil {
			log.Printf("Match recorder: failed to update match %s: %v", e.MatchID, err)
			return
		}
	}

	log.Printf("Match recorder: recorded completed match %s", e.MatchID[:8])
}

func buildMatchPlayers(matchID string, radiant, dire []coordinator.Player, captains [2]coordinator.Player) []store.MatchPlayer {
	players := make([]store.MatchPlayer, 0, len(radiant)+len(dire))
	for _, p := range radiant {
		players = append(players, store.MatchPlayer{
			MatchID:    matchID,
			SteamID:    p.SteamID,
			Team:       "radiant",
			WasCaptain: captains[0].SteamID != "" && captains[0].SteamID == p.SteamID,
			Accepted:   true,
		})
	}
	for _, p := range dire {
		players = append(players, store.MatchPlayer{
			MatchID:    matchID,
			SteamID:    p.SteamID,
			Team:       "dire",
			WasCaptain: captains[1].SteamID != "" && captains[1].SteamID == p.SteamID,
			Accepted:   true,
		})
	}
	return players
}

func (r *Recorder) fetchWithRetry(ctx context.Context, matchID uint64) (*dotaapi.MatchDetails, error) {
	delays := []time.Duration{0, 10 * time.Second, 10 * time.Second, 10 * time.Second, 30 * time.Second, 30 * time.Second}
	var lastErr error
	for i, delay := range delays {
		attempt := i + 1
		if delay > 0 {
			log.Printf("Match recorder: retrying OpenDota API fetch for match %d (attempt %d/%d) in %s", matchID, attempt, len(delays), delay)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		details, err := r.dotaAPI.GetMatchDetails(ctx, matchID)
		if err == nil {
			return details, nil
		}
		log.Printf("Match recorder: OpenDota API fetch failed for match %d on attempt %d/%d: %v", matchID, attempt, len(delays), err)
		lastErr = err
	}
	return nil, lastErr
}
