package web

import (
	"errors"
	"testing"
)

func TestSplitAndTrim(t *testing.T) {
	got := splitAndTrim(" a, b ,,c,   ,d ")
	want := []string{"a", "b", "c", "d"}

	if len(got) != len(want) {
		t.Fatalf("expected %d items, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected item %d to be %q, got %q", i, want[i], got[i])
		}
	}
}

func TestWaitForResponseImmediate(t *testing.T) {
	resp := make(chan error, 1)
	resp <- nil
	if err := waitForResponse(resp); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestWaitForResponseError(t *testing.T) {
	resp := make(chan error, 1)
	resp <- errors.New("boom")
	if err := waitForResponse(resp); err == nil || err.Error() != "boom" {
		t.Fatalf("expected boom error, got %v", err)
	}
}

