package coordinator

import "time"

type Player struct {
	SteamID         string `json:"steamId"`
	Name            string `json:"name"`
	AvatarURL       string `json:"avatarUrl"`
	CaptainPriority int    `json:"captainPriority"`
}

type MatchState int

const (
	MatchStateAccepting     MatchState = iota // Waiting for players to accept
	MatchStateDrafting                        // Captains picking players
	MatchStateWaitingForBot                   // Bot creating Dota lobby
	MatchStateInProgress                      // Game running
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

type Match struct {
	ID               string
	State            MatchState
	Players          []Player        // All 10 players in this match
	AcceptedPlayers  map[string]bool // SteamID -> accepted
	AcceptDeadline   time.Time
	PickDeadline     time.Time
	LobbyDeadline    time.Time
	Captains         [2]Player
	Radiant          []Player
	Dire             []Player
	AvailablePlayers []Player // Players not yet drafted
	CurrentPicker    int      // 0 = radiant captain, 1 = dire captain
	PickCount        int      // Number of picks made (used for timeout validation)
	DotaMatchID      uint64
}

type LobbySettings struct {
	GameMode string `json:"gameMode"` // "cm", "ap", "cd", "rd", "ar"
}

func DefaultLobbySettings() LobbySettings {
	return LobbySettings{
		GameMode: "cd",
	}
}

var ValidGameModes = map[string]string{
	"ap": "All Pick",
	"cm": "Captain's Mode",
	"cd": "Captain's Draft",
	"rd": "Random Draft",
	"ar": "All Random",
}

type State struct {
	Queue         []Player          // Players waiting for a match
	Matches       map[string]*Match // Active matches keyed by match ID
	LobbySettings LobbySettings     // Configurable lobby settings
}

func NewState() *State {
	return &State{
		Queue:         []Player{},
		Matches:       make(map[string]*Match),
		LobbySettings: DefaultLobbySettings(),
	}
}

func (s *State) IsPlayerInQueue(steamID string) bool {
	for _, p := range s.Queue {
		if p.SteamID == steamID {
			return true
		}
	}
	return false
}

func (s *State) IsPlayerInMatch(steamID string) bool {
	return s.GetPlayerMatch(steamID) != nil
}

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

func (s *State) GetMatch(matchID string) *Match {
	return s.Matches[matchID]
}

func (s *State) RemoveFromQueue(steamID string) bool {
	for i, p := range s.Queue {
		if p.SteamID == steamID {
			s.Queue = append(s.Queue[:i], s.Queue[i+1:]...)
			return true
		}
	}
	return false
}
