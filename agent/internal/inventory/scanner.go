package inventory

import "log/slog"

// InstalledApp represents a software application installed on this machine.
type InstalledApp struct {
	Name      string
	Version   string
	Publisher string
}

// Scanner reads installed software from platform-specific sources.
type Scanner struct {
	logger *slog.Logger
}

// NewScanner creates a new inventory scanner.
func NewScanner(logger *slog.Logger) *Scanner {
	return &Scanner{logger: logger}
}
