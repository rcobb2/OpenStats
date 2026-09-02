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
	// OnElevated fires when a process starts with a UAC split-token Full token
	// that was not inherited from an elevated parent. Windows only; left nil on
	// macOS. The user is the raw (uncanonicalized) process owner.
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
