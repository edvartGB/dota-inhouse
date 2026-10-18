package web

import (
	"testing"
	"time"

	"github.com/edvart/dota-inhouse/internal/coordinator"
)

func TestSSEBroadcastSkipsSlowClients(t *testing.T) {
	e := newTestEnv(t)
	e.addPlayers(1)
	hub := e.srv.sse
	slow := &SSEClient{ID: "slow", UserID: testSteamID(1), Channel: make(chan string)}
	hub.mu.Lock()
	hub.clients[slow] = true
	hub.mu.Unlock()
	done := make(chan struct{})
	go func() {
		hub.broadcast(coordinator.QueueUpdated{})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("broadcast blocked on a slow client")
	}
}
