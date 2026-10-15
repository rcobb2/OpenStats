package monitor

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/rcobb/openlabstats-agent/internal/session"
)

type credit struct {
	pid uint32
	d   time.Duration
}

type fakeTracker struct{ credits []credit }

func (f *fakeTracker) IncrementForeground(pid uint32, d time.Duration) {
	f.credits = append(f.credits, credit{pid, d})
}

type pollerHarness struct {
	p       *foregroundPoller
	tr      *fakeTracker
	idle    time.Duration
	samples []session.Sample
	legacy  uint32
	t       time.Time
}

func newHarness(threshold time.Duration) *pollerHarness {
	h := &pollerHarness{tr: &fakeTracker{}, t: time.Unix(1_000_000, 0)}
	h.p = newForegroundPoller(h.tr, ForegroundPollerConfig{
		Interval:      time.Second,
		Source:        func() []session.Sample { return h.samples },
		IdleThreshold: func() time.Duration { return threshold },
		OnIdle:        func(d time.Duration) { h.idle += d },
		Legacy:        func() uint32 { return h.legacy },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), h.t)
	return h
}

func (h *pollerHarness) tick(after time.Duration) {
	h.t = h.t.Add(after)
	h.p.tick(h.t)
}

func TestPollerIdleAtThresholdNotCredited(t *testing.T) {
	h := newHarness(10 * time.Minute)
	h.samples = []session.Sample{{PID: 1, Idle: 10 * time.Minute}, {PID: 2, Idle: 10*time.Minute - time.Millisecond}}
	h.tick(time.Second)
	if len(h.tr.credits) != 1 || h.tr.credits[0] != (credit{2, time.Second}) {
		t.Fatalf("credits = %+v, want only pid 2", h.tr.credits)
	}
	if h.idle != time.Second {
		t.Errorf("idle counter = %v, want 1s", h.idle)
	}
}

func TestPollerThresholdZeroDisablesIdle(t *testing.T) {
	h := newHarness(0)
	h.samples = []session.Sample{{PID: 1, Idle: 99 * time.Hour}}
	h.tick(time.Second)
	if len(h.tr.credits) != 1 || h.idle != 0 {
		t.Fatalf("credits=%+v idle=%v, want credited and no idle", h.tr.credits, h.idle)
	}
}

func TestPollerMultipleSamplesCreditedSeparately(t *testing.T) {
	h := newHarness(time.Minute)
	h.samples = []session.Sample{{PID: 10}, {PID: 20}, {PID: 0}}
	h.tick(time.Second)
	want := []credit{{10, time.Second}, {20, time.Second}}
	if len(h.tr.credits) != 2 || h.tr.credits[0] != want[0] || h.tr.credits[1] != want[1] {
		t.Fatalf("credits = %+v, want %+v", h.tr.credits, want)
	}
}

func TestPollerEmptySourceFallsBackToLegacy(t *testing.T) {
	h := newHarness(time.Minute)
	h.legacy = 77
	h.tick(time.Second)
	if len(h.tr.credits) != 1 || h.tr.credits[0] != (credit{77, time.Second}) {
		t.Fatalf("credits = %+v, want legacy pid 77", h.tr.credits)
	}
	// Legacy carries no idle info, so it is never idle-excluded.
	if h.idle != 0 {
		t.Errorf("idle = %v, want 0", h.idle)
	}
}

func TestPollerHelperSamplesSuppressLegacy(t *testing.T) {
	h := newHarness(time.Minute)
	h.legacy = 77
	h.samples = []session.Sample{{PID: 5}}
	h.tick(time.Second)
	if len(h.tr.credits) != 1 || h.tr.credits[0].pid != 5 {
		t.Fatalf("credits = %+v, want only helper pid 5", h.tr.credits)
	}
}

func TestPollerLargeGapCreditsNothing(t *testing.T) {
	h := newHarness(time.Minute)
	h.samples = []session.Sample{{PID: 1}, {PID: 2, Idle: time.Hour}}
	h.tick(5 * time.Second) // > 4x interval: machine slept
	if len(h.tr.credits) != 0 || h.idle != 0 {
		t.Fatalf("credits=%+v idle=%v, want nothing", h.tr.credits, h.idle)
	}
	h.tick(4 * time.Second) // exactly 4x is still plausible
	if len(h.tr.credits) != 1 || h.tr.credits[0].d != 4*time.Second {
		t.Fatalf("credits = %+v, want one 4s credit", h.tr.credits)
	}
}
