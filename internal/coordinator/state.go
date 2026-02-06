package coordinator

import "time"

// Player represents a user in the system.
type Player struct {
	SteamID   string `json:"steamId"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatarUrl"`
}

// MatchState represents the current phase of a match.
type MatchState int

const (
	MatchStateAccepting    MatchState = iota // Waiting for players to accept
	MatchStateDrafting                       // Captains picking players
	MatchStateWaitingForBot                  // Bot creating Dota lobby
	MatchStateInProgress                     // Game running
)

func (s MatchState) String() string {
	switch s {
	case MatchStateAccepting:
		return "accepting"
	case MatchStateDrafting:
		return "drafting"
	case MatchStateWaitingForBot:
		return "waiting_for_bot"
	case MatchStateInProgress:
		return "in_progress"
	default:
		return "unknown"
	}
}

// Match represents an active match being formed or played.
type Match struct {
	ID               string
	State            MatchState
	Players          []Player          // All 10 players in this match
	AcceptedPlayers  map[string]bool   // SteamID -> accepted
	AcceptDeadline   time.Time
	Captains         [2]Player
	Radiant          []Player
	Dire             []Player
	AvailablePlayers []Player // Players not yet drafted
	CurrentPicker    int      // 0 = radiant captain, 1 = dire captain
	PickCount        int      // Number of picks made (used for timeout validation)
	DotaMatchID      uint64
}

// State holds all mutable state owned by the coordinator.
type State struct {
	Queue   []Player          // Players waiting for a match
	Matches map[string]*Match // Active matches keyed by match ID
}

// NewState creates an empty initial state.
func NewState() *State {
	return &State{
		Queue:   []Player{},
		Matches: make(map[string]*Match),
	}
}

// IsPlayerInQueue checks if a player is currently in the queue.
func (s *State) IsPlayerInQueue(steamID string) bool {
	for _, p := range s.Queue {
		if p.SteamID == steamID {
			return true
		}
	}
	return false
}

// IsPlayerInMatch checks if a player is in any active match.
func (s *State) IsPlayerInMatch(steamID string) bool {
	return s.GetPlayerMatch(steamID) != nil
}

// GetPlayerMatch returns the match a player is in, or nil if not in any match.
func (s *State) GetPlayerMatch(steamID string) *Match {
	for _, match := range s.Matches {
		for _, p := range match.Players {
			if p.SteamID == steamID {
				return match
			}
		}
	}
	return nil
}

// GetMatch returns a match by ID, or nil if not found.
func (s *State) GetMatch(matchID string) *Match {
	return s.Matches[matchID]
}

// RemoveFromQueue removes a player from the queue by SteamID.
func (s *State) RemoveFromQueue(steamID string) bool {
	for i, p := range s.Queue {
		if p.SteamID == steamID {
			s.Queue = append(s.Queue[:i], s.Queue[i+1:]...)
			return true
		}
	}
	return false
}
