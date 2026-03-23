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
