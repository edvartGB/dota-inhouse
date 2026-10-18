package matchrecorder

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/edvart/dota-inhouse/internal/coordinator"
	"github.com/edvart/dota-inhouse/internal/store"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

type recorderEnv struct {
	t     *testing.T
	store *store.SQLiteStore
	coord *coordinator.Coordinator
}

func newRecorderEnv(t *testing.T) *recorderEnv {
	t.Helper()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	coord := coordinator.New()
	events := coord.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go coord.Run(ctx)
	go New(st, nil).Run(ctx, events)

	return &recorderEnv{t: t, store: st, coord: coord}
}

func (e *recorderEnv) send(build func(chan error) coordinator.Command) {
	e.t.Helper()
	resp := make(chan error, 1)
	e.coord.Send(build(resp))
	if err := <-resp; err != nil {
		e.t.Fatal(err)
	}
}

func (e *recorderEnv) match() *coordinator.Match {
	e.t.Helper()
	_, matches, _, _ := e.coord.GetState()
	for _, m := range matches {
		return m
	}
	e.t.Fatal("no active match")
	return nil
}

func (e *recorderEnv) draftedMatch() *coordinator.Match {
	e.t.Helper()
	now := time.Now()
	for i := 1; i <= 10; i++ {
		u := store.User{SteamID: fmt.Sprintf("7656119800000%04d", i), Name: fmt.Sprintf("P%02d", i), CaptainPriority: 11 - i, CreatedAt: now, UpdatedAt: now}
		if err := e.store.UpsertUser(context.Background(), &u); err != nil {
			e.t.Fatal(err)
		}
		e.send(func(r chan error) coordinator.Command {
			return coordinator.JoinQueue{Player: coordinator.Player{SteamID: u.SteamID, Name: u.Name, CaptainPriority: u.CaptainPriority}, Response: r}
		})
	}
	m := e.match()
	for _, p := range m.Players {
		pid := p.SteamID
		e.send(func(r chan error) coordinator.Command {
			return coordinator.AcceptMatch{PlayerID: pid, MatchID: m.ID, Response: r}
		})
	}
	m = e.match()
	chooser := m.Captains[m.SideChooserIndex].SteamID
	e.send(func(r chan error) coordinator.Command {
		return coordinator.ChooseSide{CaptainID: chooser, MatchID: m.ID, Side: "radiant", Response: r}
	})
	for {
		m = e.match()
		if m.State != coordinator.MatchStateDrafting {
			break
		}
		captain := m.Captains[m.CurrentPicker].SteamID
		picked := m.AvailablePlayers[0].SteamID
		e.send(func(r chan error) coordinator.Command {
			return coordinator.PickPlayer{CaptainID: captain, PickedID: picked, MatchID: m.ID, Response: r}
		})
	}
	if m.State != coordinator.MatchStateWaitingForBot {
		e.t.Fatalf("match state = %v, want waiting for bot", m.State)
	}
	return m
}

func (e *recorderEnv) waitForCompleted(matchID string) store.MatchWithPlayers {
	e.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		matches, err := e.store.ListMatchesWithPlayersPage(context.Background(), 10, 0)
		if err != nil {
			e.t.Fatal(err)
		}
		for _, m := range matches {
			if m.ID == matchID && m.State == "completed" {
				return m
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("match %s was never recorded as completed", matchID)
	return store.MatchWithPlayers{}
}

func assertCaptains(t *testing.T, got store.MatchWithPlayers, want [2]coordinator.Player) {
	t.Helper()
	if len(got.Radiant) != 5 || len(got.Dire) != 5 {
		t.Errorf("recorded teams %d/%d, want 5/5", len(got.Radiant), len(got.Dire))
	}
	if got.RadiantCaptain == nil || got.RadiantCaptain.SteamID != want[0].SteamID {
		t.Errorf("radiant captain = %+v, want %s", got.RadiantCaptain, want[0].SteamID)
	}
	if got.DireCaptain == nil || got.DireCaptain.SteamID != want[1].SteamID {
		t.Errorf("dire captain = %+v, want %s", got.DireCaptain, want[1].SteamID)
	}
}

func TestRecorderKeepsCaptainsWhenStartWasRecorded(t *testing.T) {
	e := newRecorderEnv(t)
	m := e.draftedMatch()
	e.coord.Send(coordinator.BotGameStarted{MatchID: m.ID, DotaMatchID: 0})
	winner := "radiant"
	e.coord.Send(coordinator.BotGameEnded{MatchID: m.ID, Winner: &winner})
	got := e.waitForCompleted(m.ID)
	assertCaptains(t, got, m.Captains)
	if got.Winner == nil || *got.Winner != "radiant" {
		t.Errorf("winner = %v", got.Winner)
	}
}

func TestRecorderKeepsCaptainsWhenAdminSetsResultBeforeGameStart(t *testing.T) {
	e := newRecorderEnv(t)
	m := e.draftedMatch()
	e.send(func(r chan error) coordinator.Command {
		return coordinator.AdminSetMatchResult{MatchID: m.ID, Winner: "dire", Response: r}
	})
	got := e.waitForCompleted(m.ID)
	assertCaptains(t, got, m.Captains)
	if got.Winner == nil || *got.Winner != "dire" {
		t.Errorf("winner = %v", got.Winner)
	}
}

func TestRecorderKeepsCaptainsWhenGameEndsWithoutRecordedStart(t *testing.T) {
	e := newRecorderEnv(t)
	m := e.draftedMatch()
	winner := "dire"
	e.coord.Send(coordinator.BotGameEnded{MatchID: m.ID, Winner: &winner})
	got := e.waitForCompleted(m.ID)
	assertCaptains(t, got, m.Captains)
}
