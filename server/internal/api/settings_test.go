package api

import (
	"testing"

	"github.com/rcobb/openlabstats-server/internal/store"
)

// Regression: UpdateSettings used to reject StaleTimeoutDays < 1, even
// though runStaleChecker (cmd/server/main.go) treats 0 as a valid, documented
// sentinel meaning "never auto-delete stale agents" — the validation made
// that sentinel unreachable through this endpoint, so an admin could never
// actually disable automatic deletion, only ever shorten its window.
func TestValidateSettingsAllowsZeroStaleTimeoutDays(t *testing.T) {
	s := baseValidSettings()
	s.StaleTimeoutDays = 0
	if msg := validateSettings(&s); msg != "" {
		t.Errorf("StaleTimeoutDays=0 should be accepted (means never auto-delete), got error: %q", msg)
	}
}

func TestValidateSettingsRejectsNegativeStaleTimeoutDays(t *testing.T) {
	s := baseValidSettings()
	s.StaleTimeoutDays = -1
	if msg := validateSettings(&s); msg == "" {
		t.Error("StaleTimeoutDays=-1 should be rejected, got no error")
	}
}

func TestValidateSettingsAcceptsDefaults(t *testing.T) {
	s := baseValidSettings()
	if msg := validateSettings(&s); msg != "" {
		t.Errorf("baseline valid settings should pass, got error: %q", msg)
	}
}

func baseValidSettings() store.SystemSettings {
	return store.SystemSettings{
		HeartbeatIntervalSeconds: 120,
		UpdateIntervalSeconds:    3600,
		StaleTimeoutDays:         90,
		MinAgentVersion:          "0.1.0",
		RolloutMaxConcurrent:     20,
		RolloutGraceSeconds:      900,
	}
}
