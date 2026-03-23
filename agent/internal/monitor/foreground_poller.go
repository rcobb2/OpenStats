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

	for {
		select {
		case <-ctx.Done():
			logger.Info("Foreground window poller shutting down")
			return
		case <-ticker.C:
			pid := getForegroundPID()
			if pid != 0 {
				logger.Debug("foreground window PID", "pid", pid)
				tracker.IncrementForeground(pid, interval)
			} else {
				logger.Debug("no foreground window active")
			}
		}
	}
}
