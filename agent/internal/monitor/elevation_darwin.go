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
// libproc/sysctl, which does not require the caller to own or out-privilege
// the target process — unlike proc_pidpath, which is why this check does not
// need the agent to run as root to see other users' or root's processes.
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
