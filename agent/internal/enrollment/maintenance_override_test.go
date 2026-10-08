package enrollment

import (
	"os"
	"testing"
)

// Regression: setmaintenance used to set an in-process package variable and
// exit — every CLI invocation is its own process, so it never persisted
// across commands, and the running agent service (a different process
// entirely) never consulted it either. ReadMaintenanceOverride/
// WriteMaintenanceOverride must round-trip through the filesystem, the one
// thing both the CLI and the running service actually share.
func TestMaintenanceOverrideRoundTrips(t *testing.T) {
	dir := t.TempDir()

	if got := ReadMaintenanceOverride(dir); got != nil {
		t.Fatalf("expected no override before any write, got %v", *got)
	}

	on := true
	if err := WriteMaintenanceOverride(dir, &on); err != nil {
		t.Fatalf("WriteMaintenanceOverride(on): %v", err)
	}
	if got := ReadMaintenanceOverride(dir); got == nil || !*got {
		t.Fatalf("expected override=true after writing on, got %v", got)
	}

	off := false
	if err := WriteMaintenanceOverride(dir, &off); err != nil {
		t.Fatalf("WriteMaintenanceOverride(off): %v", err)
	}
	if got := ReadMaintenanceOverride(dir); got == nil || *got {
		t.Fatalf("expected override=false after writing off, got %v", got)
	}

	if err := WriteMaintenanceOverride(dir, nil); err != nil {
		t.Fatalf("WriteMaintenanceOverride(nil): %v", err)
	}
	if got := ReadMaintenanceOverride(dir); got != nil {
		t.Fatalf("expected no override after clearing, got %v", *got)
	}
}

// Clearing an override that was never set must not error (os.Remove on a
// missing file is the expected, common case — "auto" is the default state).
func TestWriteMaintenanceOverrideClearNoOpWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := WriteMaintenanceOverride(dir, nil); err != nil {
		t.Fatalf("clearing a never-set override should not error, got %v", err)
	}
}

// An empty baseDir (a Client built without WithMaintenanceOverrideDir, or a
// CLI invocation that somehow loaded no BaseDir) must read as "no override",
// never accidentally resolve to a relative path in the current working
// directory.
func TestReadMaintenanceOverrideEmptyDirIsNoOverride(t *testing.T) {
	if got := ReadMaintenanceOverride(""); got != nil {
		t.Fatalf("expected nil for an empty baseDir, got %v", *got)
	}
}

// A corrupt or hand-edited marker file must degrade to "no override" rather
// than being misread as a forced value in either direction.
func TestReadMaintenanceOverrideUnrecognizedContentIsNoOverride(t *testing.T) {
	dir := t.TempDir()
	if err := WriteMaintenanceOverride(dir, nil); err != nil {
		t.Fatal(err)
	}
	// Write garbage directly, bypassing WriteMaintenanceOverride's own
	// "on"/"off" vocabulary.
	path := dir + "/" + maintenanceOverrideFileName
	if err := os.WriteFile(path, []byte("maybe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadMaintenanceOverride(dir); got != nil {
		t.Fatalf("expected nil for unrecognized content, got %v", *got)
	}
}
