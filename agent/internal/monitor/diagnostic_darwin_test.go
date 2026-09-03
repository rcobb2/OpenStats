//go:build darwin

package monitor

import (
	"os"
	"testing"
)

// TestGetProcBSDInfoCrossUserRequiresRoot documents and guards a load-bearing
// assumption for elevation detection: proc_pidinfo(PROC_PIDTBSDINFO) can only
// read a process's info if the caller owns it or is root — same privilege
// boundary as proc_pidpath, not looser. This was discovered the hard way: a
// CI smoke test running the agent unprivileged silently detected zero
// elevations despite a genuine sudo command succeeding, because reading any
// root-owned process failed this exact call. See elevation_darwin.go's
// rootEscalationInvoker doc comment for why this is fine in production (the
// agent already runs as root) but breaks silently for an unprivileged test
// harness.
//
// PID 1 (launchd) is always root on any running macOS system, making it a
// deterministic target with no dependency on what else happens to be running.
func TestGetProcBSDInfoCrossUserRequiresRoot(t *testing.T) {
	info, ok := getProcBSDInfo(1)

	if os.Geteuid() == 0 {
		// Running as root: same-or-higher privilege, should succeed.
		if !ok || info.uid != 0 {
			t.Errorf("getProcBSDInfo(1) as root = %+v, ok=%v; want ok=true, uid=0", info, ok)
		}
		return
	}

	// Unprivileged: this is the assumption elevation detection depends on.
	if ok {
		t.Errorf("getProcBSDInfo(1) as an unprivileged caller unexpectedly succeeded (uid=%d) — "+
			"the privilege assumption in elevation_darwin.go's doc comment no longer holds, "+
			"re-verify rootEscalationInvoker's production requirements", info.uid)
	}
}
