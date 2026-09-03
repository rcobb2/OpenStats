//go:build darwin

package monitor

import (
	"os"
	"os/user"
	"testing"
)

// fakeElevatedChildPID is a sentinel PID for the simulated sudo child. It must
// not collide with a real running process; a value far above any realistic
// macOS PID (which wrap around 99999) is safe.
const fakeElevatedChildPID = uint32(987654321)

// withFakeElevatedChild overrides listAllPIDsFn/getProcBSDInfoFn so the real,
// unmodified currentSnapshot() pipeline observes one extra process — the
// fake child — layered on top of the genuinely real running process list.
// Every other PID still resolves through the real libproc calls, so the rest
// of the snapshot behaves exactly as it does in production.
func withFakeElevatedChild(t *testing.T, ppid uint32) {
	t.Helper()
	origList := listAllPIDsFn
	origInfo := getProcBSDInfoFn
	t.Cleanup(func() {
		listAllPIDsFn = origList
		getProcBSDInfoFn = origInfo
	})

	listAllPIDsFn = func() []uint32 {
		return append(origList(), fakeElevatedChildPID)
	}
	getProcBSDInfoFn = func(pid uint32) (procBSDInfo, bool) {
		if pid == fakeElevatedChildPID {
			return procBSDInfo{exeName: "sleep", ppid: ppid, uid: 0, startSec: 1}, true
		}
		return origInfo(pid)
	}
}

// TestPollWatcherDetectsSimulatedSudoElevation drives the real poll pipeline
// (currentSnapshot, prevPIDs diffing, the onElevated hook) with a simulated
// root-owned child whose parent is this test process's own PID — a genuinely
// real, live, non-root process. This is the same scenario `sudo sleep 30`
// produces on a real system, exercised without needing an interactive
// password. See TestPollWatcherRootParentIsNotAnElevation for the negative
// case (root parent, using the real launchd PID 1).
func TestPollWatcherDetectsSimulatedSudoElevation(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skipf("could not determine current user: %v", err)
	}
	if me.Uid == "0" {
		t.Skip("test process is running as root; can't exercise the non-root-parent case")
	}

	withFakeElevatedChild(t, uint32(os.Getpid()))

	var got []string
	w, err := NewPollWatcher(NewTracker(discardLogger()), discardLogger(), WMIWatcherConfig{
		OnElevated: func(pid uint32, exeName, exePath, user string) {
			got = append(got, user)
		},
	})
	if err != nil {
		t.Fatalf("NewPollWatcher: %v", err)
	}

	// First snapshot seeds prevPIDs exactly like Run() does at startup
	// (detectElevations=false) — pre-existing processes are adopted, not
	// reported as fresh elevations.
	w.prevPIDs = w.currentSnapshot(false)
	if len(got) != 0 {
		t.Fatalf("initial snapshot must not fire OnElevated (adopt, don't count), got %v", got)
	}

	// Second pass mimics a real poll() tick: the fake child now looks "new"
	// against prevPIDs, the same way a real sudo child appears mid-poll after
	// having not existed a second ago.
	delete(w.prevPIDs, fakeElevatedChildPID)
	current := w.currentSnapshot(true)

	if len(got) != 1 {
		t.Fatalf("expected exactly one elevation to fire, got %d: %v", len(got), got)
	}
	if got[0] != me.Username {
		t.Errorf("invoking user = %q, want %q (this test process's owner)", got[0], me.Username)
	}

	// The fake child itself must not land in the usage-tracking snapshot: its
	// path is unreadable (it doesn't really exist) and its uid is 0, which is
	// exactly the "unreadable root path → system daemon" skip.
	if _, present := current[fakeElevatedChildPID]; present {
		t.Error("the fake elevated child should not appear in the usage-tracking snapshot")
	}
}

// TestPollWatcherRootParentIsNotAnElevation verifies that a root-owned
// process whose parent is also root (using launchd, PID 1 — genuinely uid 0
// on any running system) is not reported as an elevation: it's an ordinary
// part of the process tree, not a new consent.
func TestPollWatcherRootParentIsNotAnElevation(t *testing.T) {
	withFakeElevatedChild(t, 1) // launchd

	var got []string
	w, err := NewPollWatcher(NewTracker(discardLogger()), discardLogger(), WMIWatcherConfig{
		OnElevated: func(pid uint32, exeName, exePath, user string) {
			got = append(got, user)
		},
	})
	if err != nil {
		t.Fatalf("NewPollWatcher: %v", err)
	}

	w.prevPIDs = w.currentSnapshot(false)
	delete(w.prevPIDs, fakeElevatedChildPID)
	w.currentSnapshot(true)

	if len(got) != 0 {
		t.Errorf("a root process forked by launchd (root) must not count as an elevation, got %v", got)
	}
}
