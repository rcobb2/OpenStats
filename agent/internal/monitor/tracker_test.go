package monitor

import (
	"log/slog"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(noopWriter{}, nil))
}

type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestTrackerNewGroup verifies that OnProcessStart returns true when no matching
// group exists, and that the group is tracked.
func TestTrackerNewGroup(t *testing.T) {
	tr := NewTracker(discardLogger())

	isNew := tr.OnProcessStart(100, 0, "excel.exe", "/apps/excel.exe", "alice", "")
	if !isNew {
		t.Fatal("expected isNewGroup=true for first process")
	}
	if tr.ActiveCount() != 1 {
		t.Fatalf("expected 1 active group, got %d", tr.ActiveCount())
	}
}

// TestTrackerChildJoinsFamily verifies that a child process joins the parent's
// group (via family key) rather than creating a new group.
func TestTrackerChildJoinsFamily(t *testing.T) {
	tr := NewTracker(discardLogger())

	tr.OnProcessStart(100, 0, "word.exe", "/apps/word.exe", "alice", "microsoft-word")
	isNew := tr.OnProcessStart(101, 0, "word-helper.exe", "/apps/word-helper.exe", "alice", "microsoft-word")

	if isNew {
		t.Fatal("expected isNewGroup=false when family key matches existing group")
	}
	if tr.ActiveCount() != 1 {
		t.Fatalf("expected 1 group (child joined parent), got %d", tr.ActiveCount())
	}
}

// TestTrackerChildJoinsViaParentPID verifies that a child process is absorbed
// into the parent's group when the parent has a family key.
func TestTrackerChildJoinsViaParentPID(t *testing.T) {
	tr := NewTracker(discardLogger())

	tr.OnProcessStart(200, 0, "chrome.exe", "/apps/chrome.exe", "bob", "google-chrome")
	isNew := tr.OnProcessStart(201, 200, "chrome-renderer.exe", "/apps/chrome-renderer.exe", "bob", "")

	if isNew {
		t.Fatal("expected child via parentPID to join existing group")
	}
	if tr.ActiveCount() != 1 {
		t.Fatalf("expected 1 group, got %d", tr.ActiveCount())
	}
}

// TestTrackerNoFamilyDoesNotAbsorb verifies that a parent without a family key
// does NOT absorb its children (e.g., a shell launching unrelated processes).
func TestTrackerNoFamilyDoesNotAbsorb(t *testing.T) {
	tr := NewTracker(discardLogger())

	tr.OnProcessStart(300, 0, "zsh", "/bin/zsh", "carol", "") // no family key
	isNew := tr.OnProcessStart(301, 300, "python3", "/usr/bin/python3", "carol", "")

	if !isNew {
		t.Fatal("expected child of no-family parent to create new group")
	}
	if tr.ActiveCount() != 2 {
		t.Fatalf("expected 2 groups, got %d", tr.ActiveCount())
	}
}

// TestTrackerStopRetainsGroupUntilLastMember verifies that OnProcessStop returns
// nil while group members remain, and returns a session only when the last exits.
func TestTrackerStopRetainsGroupUntilLastMember(t *testing.T) {
	tr := NewTracker(discardLogger())

	tr.OnProcessStart(400, 0, "app.exe", "/app.exe", "dave", "myapp")
	tr.OnProcessStart(401, 0, "app-helper.exe", "/app-helper.exe", "dave", "myapp")

	// Stop the helper — group should still be alive (root 400 remains).
	sess := tr.OnProcessStop(401)
	if sess != nil {
		t.Fatal("expected nil session while root process still running")
	}
	if tr.ActiveCount() != 1 {
		t.Fatalf("expected 1 active group after helper exit, got %d", tr.ActiveCount())
	}

	// Stop the root — now the group ends.
	sess = tr.OnProcessStop(400)
	if sess == nil {
		t.Fatal("expected a completed session when last member exits")
	}
	if sess.ExeName != "app.exe" {
		t.Errorf("expected ExeName=app.exe, got %q", sess.ExeName)
	}
	if tr.ActiveCount() != 0 {
		t.Fatalf("expected 0 active groups after group ended, got %d", tr.ActiveCount())
	}
}

// TestTrackerSessionDurationPositive verifies that CheckpointDelta on a
// completed session is positive (actual elapsed time).
func TestTrackerSessionDurationPositive(t *testing.T) {
	tr := NewTracker(discardLogger())

	tr.OnProcessStart(500, 0, "notepad.exe", "/notepad.exe", "eve", "")
	time.Sleep(20 * time.Millisecond)
	sess := tr.OnProcessStop(500)

	if sess == nil {
		t.Fatal("expected a session")
	}
	if sess.CheckpointDelta <= 0 {
		t.Errorf("expected positive CheckpointDelta, got %v", sess.CheckpointDelta)
	}
	if sess.StopTime.Before(sess.StartTime) {
		t.Errorf("StopTime %v is before StartTime %v", sess.StopTime, sess.StartTime)
	}
}

// TestTrackerCheckpointActive verifies that CheckpointActive returns positive
// deltas for running processes and resets the timer so the next checkpoint
// only measures incremental time.
func TestTrackerCheckpointActive(t *testing.T) {
	tr := NewTracker(discardLogger())

	tr.OnProcessStart(600, 0, "excel.exe", "/excel.exe", "frank", "")
	time.Sleep(30 * time.Millisecond)

	snapshots := tr.CheckpointActive()
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snapshots))
	}
	delta1 := snapshots[0].CheckpointDelta
	if delta1 <= 0 {
		t.Errorf("expected positive first checkpoint delta, got %v", delta1)
	}

	// Second checkpoint should measure only the additional elapsed time.
	time.Sleep(20 * time.Millisecond)
	snapshots2 := tr.CheckpointActive()
	if len(snapshots2) != 1 {
		t.Fatalf("expected 1 snapshot on second checkpoint, got %d", len(snapshots2))
	}
	delta2 := snapshots2[0].CheckpointDelta
	if delta2 <= 0 {
		t.Errorf("expected positive second checkpoint delta, got %v", delta2)
	}
	// The second delta should be smaller than the first (which had ~30ms of sleep).
	if delta2 >= delta1 {
		t.Errorf("expected second delta (%v) < first delta (%v)", delta2, delta1)
	}
}

// TestTrackerForegroundAccumulates verifies that IncrementForeground accumulates
// across multiple calls and is captured in CheckpointActive.
func TestTrackerForegroundAccumulates(t *testing.T) {
	tr := NewTracker(discardLogger())

	tr.OnProcessStart(700, 0, "app.exe", "/app.exe", "grace", "")

	tr.IncrementForeground(700, 2*time.Second)
	tr.IncrementForeground(700, 3*time.Second)

	snapshots := tr.CheckpointActive()
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snapshots))
	}
	if snapshots[0].ForegroundDelta != 5*time.Second {
		t.Errorf("expected ForegroundDelta=5s, got %v", snapshots[0].ForegroundDelta)
	}

	// After checkpoint, foreground counter resets.
	snapshots2 := tr.CheckpointActive()
	if snapshots2[0].ForegroundDelta != 0 {
		t.Errorf("expected ForegroundDelta reset to 0 after checkpoint, got %v", snapshots2[0].ForegroundDelta)
	}
}

// TestTrackerForegroundOnStop verifies that foreground time accumulated before
// a process exits is reported in the final ProcessSession.
func TestTrackerForegroundOnStop(t *testing.T) {
	tr := NewTracker(discardLogger())

	tr.OnProcessStart(800, 0, "app.exe", "/app.exe", "henry", "")
	tr.IncrementForeground(800, 10*time.Second)

	sess := tr.OnProcessStop(800)
	if sess == nil {
		t.Fatal("expected a session")
	}
	if sess.ForegroundDelta != 10*time.Second {
		t.Errorf("expected ForegroundDelta=10s, got %v", sess.ForegroundDelta)
	}
}

// TestTrackerReconcileRemovesStaleGroups verifies that Reconcile removes groups
// whose processes are no longer in the running set.
func TestTrackerReconcileRemovesStaleGroups(t *testing.T) {
	tr := NewTracker(discardLogger())

	tr.OnProcessStart(900, 0, "app1.exe", "/app1.exe", "ivan", "")
	tr.OnProcessStart(901, 0, "app2.exe", "/app2.exe", "ivan", "")

	// Only PID 901 is still running.
	running := map[uint32]bool{901: true}
	stopped := tr.Reconcile(running)

	if len(stopped) != 1 {
		t.Fatalf("expected 1 stopped session from reconcile, got %d", len(stopped))
	}
	if stopped[0].ExeName != "app1.exe" {
		t.Errorf("expected stale session for app1.exe, got %q", stopped[0].ExeName)
	}
	if tr.ActiveCount() != 1 {
		t.Fatalf("expected 1 remaining group after reconcile, got %d", tr.ActiveCount())
	}
}

// TestTrackerGetProcessUser verifies user lookup by PID.
func TestTrackerGetProcessUser(t *testing.T) {
	tr := NewTracker(discardLogger())

	tr.OnProcessStart(1000, 0, "app.exe", "/app.exe", "judy", "")

	if u := tr.GetProcessUser(1000); u != "judy" {
		t.Errorf("expected user=judy, got %q", u)
	}
	if u := tr.GetProcessUser(9999); u != "" {
		t.Errorf("expected empty user for unknown PID, got %q", u)
	}
}
