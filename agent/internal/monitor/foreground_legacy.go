package monitor

import "github.com/rcobb/openlabstats-agent/internal/session"

// getForegroundPID is the legacy in-process foreground lookup, used only when
// no session helper is delivering samples. It shares the platform primitive
// with the helper (internal/session) so the cgo/Win32 code lives in one place.
// From a Windows service (session 0) it sees nothing; on macOS it sees only
// part of the console user's windows from a root daemon. The helper path is
// the accurate one — this is the fallback that keeps behavior no worse than
// before when helpers can't run.
func getForegroundPID() uint32 {
	return session.ForegroundPID()
}
