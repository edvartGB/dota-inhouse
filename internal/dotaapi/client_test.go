package dotaapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, status int, body string) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/8100000001" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	c := NewClient()
	c.baseURL = server.URL + "/"
	return c
}

func TestGetMatchDetailsDecodesWinnerAndDuration(t *testing.T) {
	for _, tc := range []struct {
		body   string
		winner string
	}{
		{`{"match_id": 8100000001, "radiant_win": true, "duration": 2732, "start_time": 1760000000, "game_mode": 2, "radiant_score": 40, "dire_score": 21}`, "radiant"},
		{`{"match_id": 8100000001, "radiant_win": false, "duration": 1500}`, "dire"},
	} {
		details, err := newTestClient(t, http.StatusOK, tc.body).GetMatchDetails(context.Background(), 8100000001)
		if err != nil {
			t.Fatalf("GetMatchDetails: %v", err)
		}
		if details.Winner() != tc.winner {
			t.Errorf("winner = %s, want %s", details.Winner(), tc.winner)
		}
		if details.MatchID != 8100000001 {
			t.Errorf("match id = %d", details.MatchID)
		}
	}

	details, _ := newTestClient(t, http.StatusOK, `{"match_id": 8100000001, "duration": 2732}`).GetMatchDetails(context.Background(), 8100000001)
	if details.Duration != 2732 {
		t.Errorf("duration = %d, want 2732", details.Duration)
	}
}

func TestGetMatchDetailsErrors(t *testing.T) {
	if _, err := newTestClient(t, http.StatusOK, `{"match_id": 0}`).GetMatchDetails(context.Background(), 8100000001); err == nil || !strings.Contains(err.Error(), "match not found") {
		t.Errorf("unparsed match: err = %v", err)
	}
	if _, err := newTestClient(t, http.StatusTooManyRequests, `rate limited`).GetMatchDetails(context.Background(), 8100000001); err == nil || !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error status: err = %v", err)
	}
	if _, err := newTestClient(t, http.StatusOK, `not json`).GetMatchDetails(context.Background(), 8100000001); err == nil {
		t.Error("expected decode error")
	}
}
