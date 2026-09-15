package dota

import "fmt"

// Game states relevant to a live match, mirroring Valve's
// DOTA_GAMERULES_STATE enum. Earlier states (init, waiting for players to
// load) aren't shown specially since GetRealtimeStats data isn't available
// yet at that point.
const (
	GameStateHeroSelection = 2
	GameStateStrategyTime  = 3
	GameStatePreGame       = 4
)

// GameClockLabel returns what to show for a live match's clock. During hero
// selection and strategy time, Valve's own game clock is negative and not
// meaningful to a viewer, so the phase name is shown instead. From pre-game
// onward, the native game clock is shown, negative until creeps spawn at
// 0:00, matching the countdown in the Dota client HUD. state is 0 until the
// bot's first live sample arrives, before which there is nothing to show.
func GameClockLabel(state int32, gameTime int32) string {
	switch {
	case state == 0:
		return ""
	case state == GameStateHeroSelection:
		return "Hero Selection"
	case state == GameStateStrategyTime:
		return "Strategy Time"
	case state < GameStatePreGame:
		return ""
	default:
		sign := ""
		t := gameTime
		if t < 0 {
			sign = "-"
			t = -t
		}
		return fmt.Sprintf("%s%d:%02d", sign, t/60, t%60)
	}
}
