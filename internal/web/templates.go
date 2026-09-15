package web

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/edvart/dota-inhouse/internal/dota"
)

func gameModeName(gameMode string) string {
	if name, ok := coordinator.ValidGameModes[gameMode]; ok {
		return name
	}
	if gameMode == "" {
		return "Unknown"
	}
	return gameMode
}

// templateFuncs returns the common template functions.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"sub": func(a, b int) int {
			return a - b
		},
		"add": func(a, b int) int {
			return a + b
		},
		"abs": func(a int) int {
			if a < 0 {
				return -a
			}
			return a
		},
		"iterate": func(n int) []int {
			if n <= 0 {
				return nil
			}
			result := make([]int, n)
			for i := range result {
				result[i] = i
			}
			return result
		},
		"percent": func(count, total int) int {
			if total == 0 {
				return 0
			}
			return (count * 100) / total
		},
		"getPlayerName": func(p coordinator.Player) string {
			return p.Name
		},
		// priorityOrder returns the two captains plus the remaining players,
		// sorted by descending CaptainPriority, for phases that want to show
		// the whole roster in the order captains were selected from.
		"priorityOrder": func(captains [2]coordinator.Player, available []coordinator.Player) []coordinator.Player {
			all := make([]coordinator.Player, 0, 2+len(available))
			all = append(all, captains[0], captains[1])
			all = append(all, available...)
			sort.SliceStable(all, func(i, j int) bool {
				return all[i].CaptainPriority > all[j].CaptainPriority
			})
			return all
		},
		// sideChoiceData lets the full page render the same side-choice panel the SSE updates use.
		"sideChoiceData": func(match *coordinator.Match, userID string) SideChoiceData {
			return SideChoiceData{
				MatchID:          match.ID,
				Captains:         match.Captains,
				SideChooserIndex: match.SideChooserIndex,
				AvailablePlayers: match.AvailablePlayers,
				Deadline:         match.SideChoiceDeadline.Format(time.RFC3339),
				UserID:           userID,
			}
		},
		"matchStateName": func(state coordinator.MatchState) string {
			switch state {
			case coordinator.MatchStateAccepting:
				return "Accepting"
			case coordinator.MatchStateChoosingSide:
				return "Choosing Side"
			case coordinator.MatchStateDrafting:
				return "Drafting"
			case coordinator.MatchStateWaitingForBot:
				return "Waiting for Lobby"
			case coordinator.MatchStateInProgress:
				return "In Game"
			default:
				return "Unknown"
			}
		},
		"matchStateClass": func(state coordinator.MatchState) string {
			switch state {
			case coordinator.MatchStateAccepting:
				return "state-accepting"
			case coordinator.MatchStateChoosingSide:
				return "state-choosing"
			case coordinator.MatchStateDrafting:
				return "state-drafting"
			case coordinator.MatchStateWaitingForBot:
				return "state-waiting"
			case coordinator.MatchStateInProgress:
				return "state-ingame"
			default:
				return ""
			}
		},
		"deref": func(s *string) string {
			if s == nil {
				return ""
			}
			return *s
		},
		"derefInt": func(v *int) int {
			if v == nil {
				return 0
			}
			return *v
		},
		"formatDuration": func(seconds *int) string {
			if seconds == nil {
				return ""
			}
			m := *seconds / 60
			s := *seconds % 60
			return fmt.Sprintf("%d:%02d", m, s)
		},
		// heroName and heroPortrait both return "" for an unpicked or unknown
		// hero, which templates use to decide whether to show art at all.
		"heroName": dota.Name,
		"heroPortrait": func(id int32) string {
			return dota.PortraitURL(id)
		},
		"remainingSeconds": func(d time.Duration) int {
			if d <= 0 {
				return 0
			}
			return int(d.Seconds())
		},
	}
}

// LoadTemplatesFromDir loads templates from a directory on the filesystem.
func LoadTemplatesFromDir(dir string) (*template.Template, error) {
	funcs := templateFuncs()

	tmpl := template.New("").Funcs(funcs)

	// Walk the templates directory and parse all .html files
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".html" {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		_, err = tmpl.Parse(string(content))
		return err
	})

	if err != nil {
		return nil, err
	}

	return tmpl, nil
}
