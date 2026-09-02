package monitor

import "time"

// WMIWatcherConfig holds configuration for the platform-specific process watcher.
// Despite the name, this struct is used by both Windows (WMIWatcher) and macOS (PollWatcher).
type WMIWatcherConfig struct {
	ExcludePatterns []string
	MinLifetime     time.Duration
	FamilyResolver  func(exeName, exePath string) string
	OnStart         func(pid uint32, exeName string, isNewGroup bool)
	OnStop          func(session *ProcessSession)
	// OnElevated fires on a genuine privilege-escalation launch: a UAC
	// split-token Full process not inherited from an elevated parent on
	// Windows, or a root-owned process launched by a non-root parent on
	// macOS. The user is the raw (uncanonicalized) human account that
	// authorized the elevation — on Windows the elevated token's own owner,
	// on macOS the parent process's owner (the elevated process itself always
	// runs as root, which is never an actionable attribution).
	OnElevated func(pid uint32, exeName, exePath, user string)
}

// RunningProcess represents a process discovered during a startup scan.
type RunningProcess struct {
	PID       uint32
	ParentPID uint32
	ExeName   string
	ExePath   string
	User      string
	FamilyKey string
}
