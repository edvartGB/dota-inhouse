package web

import (
	"html/template"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/edvart/dota-inhouse/internal/coordinator"
)

// LoadTemplates loads all templates from the filesystem.
func LoadTemplates(templatesFS fs.FS) (*template.Template, error) {
	funcs := templateFuncs()

	tmpl := template.New("").Funcs(funcs)

	// Parse all template files
	patterns := []string{
		"layouts/*.html",
		"pages/*.html",
		"partials/*.html",
	}

	for _, pattern := range patterns {
		matches, err := fs.Glob(templatesFS, pattern)
		if err != nil {
			return nil, err
		}

		for _, match := range matches {
			content, err := fs.ReadFile(templatesFS, match)
			if err != nil {
				return nil, err
			}

			_, err = tmpl.Parse(string(content))
			if err != nil {
				return nil, err
			}
		}
	}

	return tmpl, nil
}

// templateFuncs returns the common template functions.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"sub": func(a, b int) int {
			return a - b
		},
		"iterate": func(n int) []int {
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
		"matchStateName": func(state coordinator.MatchState) string {
			switch state {
			case coordinator.MatchStateAccepting:
				return "Accepting"
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
