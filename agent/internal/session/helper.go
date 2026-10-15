package session

import (
	"context"
	"io"
	"time"
)

// HelperInterval is how often the helper reports.
const HelperInterval = 5 * time.Second

// RunHelper is the body of `openlabstats-agent session-helper`: every interval
// it writes one JSON sample line to w. It returns nil as soon as a write fails
// (the service closed the pipe or died, so there is no one left to report to)
// or when ctx is cancelled. fg and idle are the platform primitives.
func RunHelper(ctx context.Context, w io.Writer, interval time.Duration, fg func() uint32, idle func() time.Duration) error {
	emit := func() bool {
		_, err := w.Write(EncodeLine(Sample{PID: fg(), Idle: idle()}))
		return err == nil
	}
	if !emit() {
		return nil
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if !emit() {
				return nil
			}
		}
	}
}
