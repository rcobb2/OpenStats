package monitor

import (
	"context"
	"log/slog"
	"time"
)

// RunForegroundPoller polls the foreground window every interval and attributes
// active time to the corresponding process group in the Tracker.
// getForegroundPID is provided by a platform-specific file (foreground.go or foreground_darwin.go).
func RunForegroundPoller(ctx context.Context, tracker *Tracker, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logger.Info("started foreground window poller", "interval", interval)

	// Credit actual elapsed wall time between ticks, not the nominal interval:
	// time.Ticker drops missed ticks rather than queuing them, so a GC pause,
	// scheduling delay, or sleep/wake gap would otherwise still credit a flat
	// `interval` regardless of how much real time passed.
	last := time.Now()
	// lastLoggedPID/haveLoggedPID gate the per-tick debug line to foreground
	// transitions only. Logging the polled PID (or "no window active") on
	// every tick, forever, at this interval is the same unbounded-growth
	// pattern as the darwin process snapshot logging — see proc_darwin.go.
	var lastLoggedPID uint32
	haveLoggedPID := false
	for {
		select {
		case <-ctx.Done():
			logger.Info("Foreground window poller shutting down")
			return
		case now := <-ticker.C:
			elapsed := now.Sub(last)
			last = now
			// Cap at a small multiple of the nominal interval so a long
			// suspend/resume gap doesn't credit hours of foreground time to
			// whatever happened to be focused right before the machine slept.
			if elapsed > interval*4 {
				elapsed = interval
			}
			pid := getForegroundPID()
			if pid != 0 {
				tracker.IncrementForeground(pid, elapsed)
			}
			if !haveLoggedPID || pid != lastLoggedPID {
				if pid != 0 {
					logger.Debug("foreground window PID", "pid", pid)
				} else {
					logger.Debug("no foreground window active")
				}
				lastLoggedPID = pid
				haveLoggedPID = true
			}
		}
	}
}
