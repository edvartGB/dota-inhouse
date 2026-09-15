package dota

import (
	"net/http"
	"testing"
)

// TestMatchHeroURLsResolve checks the generated slugs against Valve's CDN using
// the 16 heroes drafted in match 8995927103. Slugs are not derivable from
// display names (Windranger is "windrunner", Nature's Prophet is "furion"), so
// this guards the generated table against a bad codegen run.
func TestMatchHeroURLsResolve(t *testing.T) {
	ids := []int32{74, 126, 75, 21, 112, 67, 43, 29, 68, 135, 53, 11, 99, 86, 113, 25}

	for _, id := range ids {
		name := Name(id)
		if name == "" {
			t.Errorf("hero id %d missing from table", id)
			continue
		}
		for label, url := range map[string]string{"portrait": PortraitURL(id), "icon": IconURL(id)} {
			resp, err := http.Head(url)
			if err != nil {
				t.Errorf("%s (%d) %s: %v", name, id, label, err)
				continue
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s (%d) %s: HTTP %d for %s", name, id, label, resp.StatusCode, url)
			}
		}
	}
}

func TestUnknownHeroDegradesToEmpty(t *testing.T) {
	// Hero id 0 is Valve's "not picked yet" sentinel and must not produce a URL.
	for _, id := range []int32{0, -1, 99999} {
		if got := PortraitURL(id); got != "" {
			t.Errorf("PortraitURL(%d) = %q, want empty", id, got)
		}
		if got := Name(id); got != "" {
			t.Errorf("Name(%d) = %q, want empty", id, got)
		}
	}
}
