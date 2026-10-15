package session

import (
	"testing"
	"time"
)

func TestNextSpawnAllowed(t *testing.T) {
	start := time.Unix(1000, 0)
	exit := start.Add(2 * time.Second)

	if got := nextSpawnAllowed(start, exit, 0); !got.Equal(start.Add(30 * time.Second)) {
		t.Errorf("normal: got %v", got.Sub(start))
	}
	if got := nextSpawnAllowed(start, exit, quickFailLimit-1); !got.Equal(start.Add(30 * time.Second)) {
		t.Errorf("below limit: got %v", got.Sub(start))
	}
	if got := nextSpawnAllowed(start, exit, quickFailLimit); !got.Equal(exit.Add(10 * time.Minute)) {
		t.Errorf("at limit: got %v", got.Sub(start))
	}
	// A long-lived helper that exits late must not be penalized below 30s since start.
	late := start.Add(2 * time.Hour)
	if got := nextSpawnAllowed(start, late, 0); !got.Equal(start.Add(30 * time.Second)) {
		t.Errorf("old start: got %v", got.Sub(start))
	}
}

func TestNextQuickFails(t *testing.T) {
	start := time.Unix(1000, 0)
	n := 0
	var tripped bool
	for i := 1; i <= quickFailLimit+1; i++ {
		n, tripped = nextQuickFails(n, start, start.Add(3*time.Second))
		if want := i == quickFailLimit; tripped != want {
			t.Fatalf("exit %d: tripped=%v want %v", i, tripped, want)
		}
	}
	if n != quickFailLimit+1 {
		t.Errorf("count = %d", n)
	}
	// A helper that lived at least quickExit resets the streak.
	if n, tripped = nextQuickFails(4, start, start.Add(quickExit)); n != 0 || tripped {
		t.Errorf("long-lived exit: n=%d tripped=%v", n, tripped)
	}
}
