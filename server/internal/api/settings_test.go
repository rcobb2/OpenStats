package api

import (
	"strings"
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
		IdleThresholdSeconds:     900,
		MinAgentVersion:          "0.1.0",
		RolloutMaxConcurrent:     20,
		RolloutGraceSeconds:      900,
	}
}

func TestValidateSettingsIdleThreshold(t *testing.T) {
	for _, tc := range []struct {
		v  int
		ok bool
	}{{0, true}, {60, true}, {600, true}, {86400, true}, {30, false}, {59, false}, {86401, false}, {-1, false}} {
		s := baseValidSettings()
		s.IdleThresholdSeconds = tc.v
		if msg := validateSettings(&s); (msg == "") != tc.ok {
			t.Errorf("IdleThresholdSeconds=%d: ok=%v, got msg %q", tc.v, tc.ok, msg)
		}
	}
}

// An absent idleThresholdSeconds must be reported as absent (handler then keeps
// the stored value); an explicit 0 must be honored (disable).
func TestDecodeSettingsUpdateIdleThreshold(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		present    bool
		want       int
		valid      bool
	}{
		{"absent", `{"heartbeatIntervalSeconds":120}`, false, 0, true},
		{"zero", `{"idleThresholdSeconds":0}`, true, 0, true},
		{"600", `{"idleThresholdSeconds":600}`, true, 600, true},
		{"30 rejected", `{"idleThresholdSeconds":30}`, true, 30, false},
	} {
		got, present, err := decodeSettingsUpdate(strings.NewReader(tc.body))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if present != tc.present || got.IdleThresholdSeconds != tc.want {
			t.Errorf("%s: present=%v value=%d, want %v/%d", tc.name, present, got.IdleThresholdSeconds, tc.present, tc.want)
		}
		if tc.present {
			full := baseValidSettings()
			full.IdleThresholdSeconds = got.IdleThresholdSeconds
			if ok := validateSettings(&full) == ""; ok != tc.valid {
				t.Errorf("%s: valid=%v, want %v", tc.name, ok, tc.valid)
			}
		}
	}
}
