//go:build darwin

package monitor

// rootEscalationInvoker inspects whether a newly-started process running as
// uid 0 represents a genuine privilege escalation, and if so, returns the
// human user who invoked it — the owner of the parent process (the shell that
// ran sudo, the app that called AuthorizationExecuteWithPrivileges). The
// elevated process's own owner is always root, which is never an actionable
// attribution.
//
// getProcBSDInfo (defined in proc_darwin.go) reads process metadata via
// proc_pidinfo(PROC_PIDTBSDINFO), which — like proc_pidpath — requires the
// caller to own the target process or be root; an unprivileged caller gets
// ok=false for any other user's process, root's included (confirmed via
// TestGetProcBSDInfoCrossUserRequiresRoot). This is not a gap in practice:
// the agent already runs as root in production (a LaunchDaemon, needed for
// the rest of its cross-user usage tracking on shared lab machines), so
// rootEscalationInvoker sees every parent process regardless of owner. An
// agent run unprivileged (e.g. ad hoc local testing) will silently detect no
// elevations at all, the same as it silently misses other users' app usage.
func rootEscalationInvoker(childUID, parentPID uint32) (invokingUser string, escalated bool) {
	parentInfo, ok := getProcBSDInfo(parentPID)
	if !ok {
		return "", false
	}
	if !shouldCountRootLaunch(childUID, parentInfo.uid, true) {
		return "", false
	}
	return resolveUID(parentInfo.uid), true
}
