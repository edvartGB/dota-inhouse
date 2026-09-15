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
	MatchStateChoosingSide                    // Side chooser selecting Radiant or Dire
	MatchStateDrafting                        // Captains picking players
	MatchStateWaitingForBot                   // Bot creating Dota lobby
	MatchStateInProgress                      // Game running
)

func (s MatchState) String() string {
	switch s {
	case MatchStateAccepting:
		return "accepting"
	case MatchStateChoosingSide:
		return "choosing_side"
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
	ID                 string
	State              MatchState
	Players            []Player        // All 10 players in this match
	AcceptedPlayers    map[string]bool // SteamID -> accepted
	AcceptDeadline     time.Time
	SideChoiceDeadline time.Time
	PickDeadline       time.Time
	LobbyDeadline      time.Time
	LobbyPaused        bool
	LobbyRemaining     time.Duration
	Captains           [2]Player
	Radiant            []Player
	Dire               []Player
	AvailablePlayers   []Player // Players not yet drafted
	SideChooserIndex   int      // Which captain chooses side
	CurrentPicker      int      // 0 = radiant captain, 1 = dire captain
	PickCount          int      // Number of picks made (used for timeout validation)
	DotaMatchID        uint64
	GameStartedAt      *time.Time
	// Heroes maps Steam64 id to the hero that player is on in the live game.
	// Nil until the bot reports a draft. A player missing from the map has no
	// hero yet, which is the normal state during a draft.
	Heroes map[string]int32
}

// HeroID returns the live hero for a player, or 0 if they have none yet.
// Templates call this so a nil map is not a special case at the call site.
func (m *Match) HeroID(steamID string) int32 {
	return m.Heroes[steamID]
}

type LobbySettings struct {
	GameMode string `json:"gameMode"` // "cm", "ap", "cd", "rd", "ar"
	LeagueID uint32 `json:"leagueId"` // 0 means no league
}

func DefaultLobbySettings() LobbySettings {
	return LobbySettings{
		GameMode: "cd",
		LeagueID: 0,
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
	QueueOpen     bool              // Whether new joins are allowed
}

func NewState() *State {
	return &State{
		Queue:         []Player{},
		Matches:       make(map[string]*Match),
		LobbySettings: DefaultLobbySettings(),
		QueueOpen:     true, // open by default on startup
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
