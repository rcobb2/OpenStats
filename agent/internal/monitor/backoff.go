package monitor

import (
	"context"
	"time"
)

const (
	retryBackoffMin = time.Second
	retryBackoffMax = 30 * time.Second
)

// backoff is a doubling delay (min..max) for retrying a failed subscription.
type backoff struct {
	min, max, cur time.Duration
}

func newBackoff(min, max time.Duration) *backoff {
	return &backoff{min: min, max: max}
}

// Next returns the delay to wait now and advances the sequence.
func (b *backoff) Next() time.Duration {
	if b.cur == 0 {
		b.cur = b.min
	} else {
		b.cur *= 2
		if b.cur > b.max {
			b.cur = b.max
		}
	}
	return b.cur
}

// Reset returns the sequence to its minimum (call after a success).
func (b *backoff) Reset() { b.cur = 0 }

// sleepCtx waits d or until ctx is done; it reports whether the full wait elapsed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
