//go:build darwin

package monitor

import "testing"

// TestDiagnosticRootProcessVisibility is a throwaway CI diagnostic: does
// getProcBSDInfo (proc_pidinfo/PROC_PIDTBSDINFO) succeed for a process this
// caller doesn't own? PID 1 (launchd) is always root on any running macOS
// system, so this isolates whether proc_pidinfo has different privilege
// semantics than sysctl(KERN_PROC) (what `ps` uses), independent of any
// sudo-timing question. Run explicitly via -run in CI; not part of the
// regular suite's signal.
func TestDiagnosticRootProcessVisibility(t *testing.T) {
	info, ok := getProcBSDInfo(1)
	t.Logf("getProcBSDInfo(1) [launchd, always root] = %+v, ok=%v", info, ok)
	if !ok {
		t.Log("getProcBSDInfo failed entirely for a cross-user (root) pid — proc_pidinfo is gated more strictly than sysctl(KERN_PROC)")
	} else if info.uid != 0 {
		t.Logf("getProcBSDInfo succeeded but reported uid=%d instead of 0 — pbi_uid may not mean what we assume", info.uid)
	} else {
		t.Log("getProcBSDInfo correctly reports launchd as uid 0 — cross-user info works fine")
	}
}
