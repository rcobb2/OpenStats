//go:build darwin

package monitor

// rootEscalationInvoker inspects whether a newly-started process running as
// uid 0 represents a genuine privilege escalation, and if so, returns the
// human user who invoked it — found by walking up the process tree until a
// non-root ancestor turns up (see findEscalatingAncestor for why one hop
// isn't enough). The elevated process's own owner is always root, which is
// never an actionable attribution.
//
// getProcBSDInfo (defined in proc_darwin.go) reads process metadata via
// proc_pidinfo(PROC_PIDTBSDINFO), which — like proc_pidpath — requires the
// caller to own the target process or be root; an unprivileged caller gets
// ok=false for any other user's process, root's included (confirmed via
// TestGetProcBSDInfoCrossUserRequiresRoot). This is not a gap in practice:
// the agent already runs as root in production (a LaunchDaemon, needed for
// the rest of its cross-user usage tracking on shared lab machines), so this
// walk sees every ancestor regardless of owner. An agent run unprivileged
// (e.g. ad hoc local testing) will silently detect no elevations at all, the
// same as it silently misses other users' app usage.
func rootEscalationInvoker(childUID, startPID uint32) (invokingUser string, escalated bool) {
	if childUID != 0 {
		return "", false
	}
	uid, found := findEscalatingAncestor(startPID, func(pid uint32) (uint32, uint32, bool) {
		info, ok := getProcBSDInfo(pid)
		return info.uid, info.ppid, ok
	})
	if !found {
		return "", false
	}
	return resolveUID(uid), true
}
