package matchrecorder

import (
	"context"
	"log"
	"time"

	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/edvart/dota-inhouse/internal/dotaapi"
	"github.com/edvart/dota-inhouse/internal/store"
)

// Recorder saves completed matches to the database.
type Recorder struct {
	store     store.Store
	dotaAPI   *dotaapi.Client
}

// New creates a new match recorder.
func New(s store.Store, dotaAPI *dotaapi.Client) *Recorder {
	return &Recorder{store: s, dotaAPI: dotaAPI}
}

// Run listens for match events and records them.
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
	// Create initial match record
	match := &store.Match{
		ID:          e.MatchID,
		DotaMatchID: e.DotaMatchID,
		State:       "in_progress",
		StartedAt:   time.Now(),
	}

	if err := r.store.CreateMatch(ctx, match); err != nil {
		log.Printf("Match recorder: failed to create match %s: %v", e.MatchID, err)
	} else {
		log.Printf("Match recorder: recorded match start %s", e.MatchID[:8])
	}
}

func (r *Recorder) recordMatchCompleted(ctx context.Context, e coordinator.MatchCompleted) {
	now := time.Now()

	// Try to fetch match details from Dota API if we have a match ID
	var winner *string
	var duration *int
	if e.DotaMatchID != 0 && r.dotaAPI != nil {
		details, err := r.dotaAPI.GetMatchDetails(ctx, e.DotaMatchID)
		if err != nil {
			log.Printf("Match recorder: failed to fetch Dota API details for match %d: %v", e.DotaMatchID, err)
			// Fall back to provided winner
			winner = e.Winner
		} else {
			w := details.Winner()
			winner = &w
			duration = &details.Duration
			log.Printf("Match recorder: fetched Dota API details - winner: %s, duration: %s", w, details.DurationFormatted())
		}
	} else {
		winner = e.Winner
	}

	// Check if match exists, create or update
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
		if err := r.store.CreateMatch(ctx, match); err != nil {
			log.Printf("Match recorder: failed to create completed match %s: %v", e.MatchID, err)
			return
		}
	} else {
		// Update existing match
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

	// Record players
	for _, p := range e.Radiant {
		isCaptain := len(e.Radiant) > 0 && e.Radiant[0].SteamID == p.SteamID
		mp := &store.MatchPlayer{
			MatchID:    e.MatchID,
			SteamID:    p.SteamID,
			Team:       "radiant",
			WasCaptain: isCaptain,
			Accepted:   true,
		}
		if err := r.store.AddMatchPlayer(ctx, mp); err != nil {
			log.Printf("Match recorder: failed to add player %s to match %s: %v", p.SteamID, e.MatchID[:8], err)
		}
	}

	for _, p := range e.Dire {
		isCaptain := len(e.Dire) > 0 && e.Dire[0].SteamID == p.SteamID
		mp := &store.MatchPlayer{
			MatchID:    e.MatchID,
			SteamID:    p.SteamID,
			Team:       "dire",
			WasCaptain: isCaptain,
			Accepted:   true,
		}
		if err := r.store.AddMatchPlayer(ctx, mp); err != nil {
			log.Printf("Match recorder: failed to add player %s to match %s: %v", p.SteamID, e.MatchID[:8], err)
		}
	}

	log.Printf("Match recorder: recorded completed match %s", e.MatchID[:8])
}
