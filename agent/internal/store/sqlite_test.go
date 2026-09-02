package store

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := New(path, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRecordElevationAndSessionTotals(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "agent.db"))

	if err := s.RecordElevation("setup.exe", "Setup", "Installer", "COLGATE\\jdoe", "LAB-PC-01"); err != nil {
		t.Fatalf("RecordElevation: %v", err)
	}
	if err := s.RecordElevation("setup.exe", "Setup", "Installer", "COLGATE\\jdoe", "LAB-PC-01"); err != nil {
		t.Fatalf("RecordElevation: %v", err)
	}
	start := time.Now().Add(-time.Minute)
	if err := s.RecordSession(123, "setup.exe", `C:\tmp\setup.exe`, "Setup", "Installer", "Acme",
		"COLGATE\\jdoe", "LAB-PC-01", start, time.Now(), 10); err != nil {
		t.Fatalf("RecordSession: %v", err)
	}

	totals, err := s.GetUsageTotals()
	if err != nil {
		t.Fatalf("GetUsageTotals: %v", err)
	}
	if len(totals) != 1 {
		t.Fatalf("expected 1 totals row, got %d", len(totals))
	}
	if totals[0].TotalElevations != 2 {
		t.Errorf("TotalElevations = %d, want 2", totals[0].TotalElevations)
	}
	if totals[0].TotalLaunches != 1 {
		t.Errorf("TotalLaunches = %d, want 1", totals[0].TotalLaunches)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")

	s := openTestStore(t, path)
	if err := s.RecordElevation("regedit.exe", "Registry Editor", "System", "admin", "LAB-PC-02"); err != nil {
		t.Fatalf("RecordElevation: %v", err)
	}
	s.Close()

	// Reopening runs migrate() again over the existing schema.
	s2 := openTestStore(t, path)
	totals, err := s2.GetUsageTotals()
	if err != nil {
		t.Fatalf("GetUsageTotals after reopen: %v", err)
	}
	if len(totals) != 1 || totals[0].TotalElevations != 1 {
		t.Fatalf("expected elevation total to survive reopen, got %+v", totals)
	}
}

// TestElevationColumnAddedToExistingDB simulates a database created by an agent
// version that predates the total_elevations column.
func TestElevationColumnAddedToExistingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")

	s := openTestStore(t, path)
	if _, err := s.db.Exec("ALTER TABLE app_usage_totals DROP COLUMN total_elevations"); err != nil {
		t.Fatalf("drop column: %v", err)
	}
	s.Close()

	s2 := openTestStore(t, path)
	if err := s2.RecordElevation("cmd.exe", "Command Prompt", "System", "admin", "LAB-PC-03"); err != nil {
		t.Fatalf("RecordElevation after re-migration: %v", err)
	}
	totals, err := s2.GetUsageTotals()
	if err != nil {
		t.Fatalf("GetUsageTotals: %v", err)
	}
	if len(totals) != 1 || totals[0].TotalElevations != 1 {
		t.Fatalf("expected TotalElevations=1 after column re-add, got %+v", totals)
	}
}
