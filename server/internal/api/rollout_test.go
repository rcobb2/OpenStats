package api

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rcobb/openlabstats-server/internal/config"
	"github.com/rcobb/openlabstats-server/internal/store"
)

// isInMaintenanceWindow gates every staggered rollout, so its edge cases matter:
// an unset window must mean "always" (preserving the historical no-restriction
// behavior), a zero-length window "never", and windows that wrap midnight must
// work in both halves.
func TestIsInMaintenanceWindow(t *testing.T) {
	at := func(hh, mm int) time.Time { return time.Date(2026, 9, 2, hh, mm, 0, 0, time.Local) }
	tests := []struct {
		name       string
		start, end string
		now        time.Time
		want       bool
	}{
		{"empty = always", "", "", at(3, 0), true},
		{"empty end = always", "22:00", "", at(3, 0), true},
		{"zero-length = never", "02:00", "02:00", at(2, 0), false},
		{"inside normal window", "01:00", "05:00", at(3, 0), true},
		{"before normal window", "01:00", "05:00", at(0, 30), false},
		{"after normal window", "01:00", "05:00", at(6, 0), false},
		{"inside midnight-wrap (late)", "22:00", "04:00", at(23, 30), true},
		{"inside midnight-wrap (early)", "22:00", "04:00", at(2, 0), true},
		{"outside midnight-wrap", "22:00", "04:00", at(12, 0), false},
		{"invalid = always", "nonsense", "04:00", at(12, 0), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isInMaintenanceWindow(tt.start, tt.end, tt.now); got != tt.want {
				t.Errorf("isInMaintenanceWindow(%q,%q,%v) = %v, want %v", tt.start, tt.end, tt.now, got, tt.want)
			}
		})
	}
}

func TestPlatformLabel(t *testing.T) {
	tests := []struct{ os, want string }{
		{"macOS 14.3.1", "macOS"},
		{"darwin", "macOS"},
		{"14.3.1", "macOS"}, // bare numeric = macOS (agents pre prefix-fix)
		{"26.3", "macOS"},   // bare numeric
		{"Windows 11", "Windows"},
		{"Windows Server 2022", "Windows"},
		{"something else", "Windows"}, // default
	}
	for _, tt := range tests {
		if got := platformLabel(tt.os); got != tt.want {
			t.Errorf("platformLabel(%q) = %q, want %q", tt.os, got, tt.want)
		}
	}
}

// newTestServerWithInstallers builds a Server whose PublicDir/installers holds
// the given filenames (empty files), for target-resolution tests.
func newTestServerWithInstallers(t *testing.T, filenames ...string) *Server {
	t.Helper()
	dir := t.TempDir()
	instDir := filepath.Join(dir, "installers")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, f := range filenames {
		if err := os.WriteFile(filepath.Join(instDir, f), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	return &Server{
		cfg:    &config.Config{Server: config.ServerConfig{PublicDir: dir}},
		logger: slog.Default(),
	}
}

// GetLatestInstallerVersion must pick the newest version and the right platform's
// extension, since it defines the rollout target per platform.
func TestGetLatestInstallerVersion(t *testing.T) {
	s := newTestServerWithInstallers(t,
		"openlabstats-agent-0.1.10-universal.pkg",
		"openlabstats-agent-0.4.0-universal.pkg",
		"openlabstats-agent-0.2.0.msi",
		"openlabstats-agent-0.4.0.msi",
	)
	if got := s.GetLatestInstallerVersion("macOS 14.3"); got != "0.4.0" {
		t.Errorf("macOS target = %q, want 0.4.0", got)
	}
	if got := s.GetLatestInstallerVersion("Windows 11"); got != "0.4.0" {
		t.Errorf("Windows target = %q, want 0.4.0", got)
	}
	// Newest .pkg is 0.4.0 even though an older .pkg is also present.
	empty := newTestServerWithInstallers(t)
	if got := empty.GetLatestInstallerVersion("macOS"); got != "" {
		t.Errorf("no installers should yield empty target, got %q", got)
	}
}

// decideRolloutUpdate's store-free branches: auto-update off, and below-target
// but outside the maintenance window. Both must return "" without touching the
// store (the DB-backed claim/release paths are covered by the live/simulated
// rollout verification). A nil store here proves those paths don't dereference it.
func TestDecideRolloutUpdateStoreFreeBranches(t *testing.T) {
	s := newTestServerWithInstallers(t, "openlabstats-agent-0.4.0-universal.pkg")
	req := RegisterAgentRequest{ID: "mac1", OSVersion: "macOS 14.3", AgentVersion: "0.1.10"}

	// Auto-update disabled → no offer, no store use.
	off := &store.SystemSettings{AutoUpdateEnabled: false}
	if url := s.decideRolloutUpdate(context.Background(), req, off); url != "" {
		t.Errorf("auto-update disabled should offer nothing, got %q", url)
	}

	// Enabled, agent below target, but outside a real maintenance window → defer,
	// no store use. Window 03:00–03:00 is zero-length (never), guaranteeing "outside".
	outside := &store.SystemSettings{
		AutoUpdateEnabled:      true,
		RolloutMaxConcurrent:   20,
		RolloutGraceSeconds:    900,
		MaintenanceWindowStart: "03:00",
		MaintenanceWindowEnd:   "03:00",
	}
	if url := s.decideRolloutUpdate(context.Background(), req, outside); url != "" {
		t.Errorf("outside maintenance window should defer, got %q", url)
	}

	// nil settings → no offer.
	if url := s.decideRolloutUpdate(context.Background(), req, nil); url != "" {
		t.Errorf("nil settings should offer nothing, got %q", url)
	}
}
