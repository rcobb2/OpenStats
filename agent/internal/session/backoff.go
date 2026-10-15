package session

import "time"

// Restart policy for a session's helper.
const (
	minSpawnInterval = 30 * time.Second // at most one spawn per session per this long
	quickExit        = 10 * time.Second // living less than this counts as a quick failure
	quickFailLimit   = 5                // consecutive quick failures before the long back-off
	longBackoff      = 10 * time.Minute
)

// nextSpawnAllowed returns the earliest time a helper may be spawned again. It
// is pure so the policy can be tested without processes or clocks: normally one
// spawn per minSpawnInterval, and after quickFailLimit consecutive quick exits
// it waits longBackoff from the last exit instead.
func nextSpawnAllowed(lastStart, lastExit time.Time, quickFails int) time.Time {
	next := lastStart.Add(minSpawnInterval)
	if quickFails >= quickFailLimit {
		if t := lastExit.Add(longBackoff); t.After(next) {
			next = t
		}
	}
	return next
}

// nextQuickFails updates the consecutive-quick-failure count after an exit and
// reports whether this exit is the one that trips the long back-off (so the
// caller logs the warning exactly once per trip).
func nextQuickFails(prev int, started, exited time.Time) (count int, tripped bool) {
	if exited.Sub(started) >= quickExit {
		return 0, false
	}
	count = prev + 1
	return count, count == quickFailLimit
}
