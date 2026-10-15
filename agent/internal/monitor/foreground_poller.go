package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/rcobb/openlabstats-agent/internal/session"
)

// foregroundCreditor is the part of Tracker the poller needs.
type foregroundCreditor interface {
	IncrementForeground(pid uint32, duration time.Duration)
}

// ForegroundPollerConfig wires the poller to its inputs and outputs.
type ForegroundPollerConfig struct {
	Interval time.Duration
	// Source returns the fresh, validated samples from session helpers, one
	// per user session. Empty means no helper is delivering.
	Source func() []session.Sample
	// IdleThreshold returns the current idle cutoff; <= 0 disables idle
	// exclusion. Read every tick so server changes apply without a restart.
	IdleThreshold func() time.Duration
	// OnIdle is told how long a present-but-idle foreground window went
	// uncredited (feeds openlabstats_foreground_idle_seconds_total).
	OnIdle func(time.Duration)
	// Legacy overrides the in-process foreground lookup (tests). Defaults to
	// the platform getForegroundPID.
	Legacy func() uint32
}

// foregroundPoller holds the per-tick logic separate from the timer loop so
// it can be driven with explicit timestamps in tests.
type foregroundPoller struct {
	cfg     ForegroundPollerConfig
	tracker foregroundCreditor
	logger  *slog.Logger

	last time.Time
	// lastLogged gates the per-tick debug line to foreground transitions
	// only. Logging the polled PID (or "no window active") on every tick,
	// forever, at this interval is the same unbounded-growth pattern as the
	// darwin process snapshot logging — see proc_darwin.go.
	lastLogged string
	haveLogged bool
}

// RunForegroundPoller attributes foreground time to process groups in the
// Tracker. Samples come from per-session helpers (which can see the user's
// desktop and input state); when none are delivering it falls back to the
// legacy in-process lookup, which has no idle information.
func RunForegroundPoller(ctx context.Context, tracker *Tracker, cfg ForegroundPollerConfig, logger *slog.Logger) {
	p := newForegroundPoller(tracker, cfg, logger, time.Now())
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	logger.Info("started foreground window poller", "interval", cfg.Interval)

	for {
		select {
		case <-ctx.Done():
			logger.Info("Foreground window poller shutting down")
			return
		case now := <-ticker.C:
			p.tick(now)
		}
	}
}

func newForegroundPoller(tracker foregroundCreditor, cfg ForegroundPollerConfig, logger *slog.Logger, start time.Time) *foregroundPoller {
	if cfg.Legacy == nil {
		cfg.Legacy = getForegroundPID
	}
	if cfg.IdleThreshold == nil {
		cfg.IdleThreshold = func() time.Duration { return 0 }
	}
	if cfg.OnIdle == nil {
		cfg.OnIdle = func(time.Duration) {}
	}
	if cfg.Source == nil {
		cfg.Source = func() []session.Sample { return nil }
	}
	return &foregroundPoller{cfg: cfg, tracker: tracker, logger: logger, last: start}
}

func (p *foregroundPoller) tick(now time.Time) {
	// Credit actual elapsed wall time between ticks, not the nominal interval:
	// time.Ticker drops missed ticks rather than queuing them, so a GC pause,
	// scheduling delay, or sleep/wake gap would otherwise still credit a flat
	// interval regardless of how much real time passed.
	elapsed := now.Sub(p.last)
	p.last = now
	// A gap far beyond the nominal interval means the machine slept or the
	// process was suspended. Credit nothing for it: nobody was using whatever
	// happened to be focused before the machine slept, and crediting even one
	// interval would be a guess.
	plausible := elapsed <= p.cfg.Interval*4

	samples := p.cfg.Source()
	if len(samples) == 0 {
		samples = []session.Sample{{PID: p.cfg.Legacy()}}
	}
	threshold := p.cfg.IdleThreshold()

	pids := make([]string, 0, len(samples))
	for _, s := range samples {
		pids = append(pids, fmt.Sprint(s.PID))
		if s.PID == 0 || !plausible {
			continue
		}
		if threshold > 0 && s.Idle >= threshold {
			// Someone left a window focused and walked away. Keep the time
			// visible as its own series instead of silently dropping it.
			p.cfg.OnIdle(elapsed)
			continue
		}
		p.tracker.IncrementForeground(s.PID, elapsed)
	}

	key := strings.Join(pids, ",")
	if !p.haveLogged || key != p.lastLogged {
		if len(samples) == 1 && samples[0].PID == 0 {
			p.logger.Debug("no foreground window active")
		} else {
			p.logger.Debug("foreground window PIDs", "pids", key)
		}
		p.lastLogged, p.haveLogged = key, true
	}
}
