//go:build windows

package monitor

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// getTokenElevationType returns the TOKEN_ELEVATION_TYPE for pid. ok=false when
// the process is already gone or its token is inaccessible (short-lived process
// race, protected/PPL process) — callers treat that as "unknown", never as an
// error worth surfacing.
func getTokenElevationType(pid uint32) (elevType uint32, ok bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(h)

	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return 0, false
	}
	defer tok.Close()

	var ret uint32
	err = windows.GetTokenInformation(tok, windows.TokenElevationType,
		(*byte)(unsafe.Pointer(&elevType)), uint32(unsafe.Sizeof(elevType)), &ret)
	if err != nil {
		return 0, false
	}
	return elevType, true
}

// elevationEvaluation carries the intermediate token-check results behind an
// isUACElevatedLaunch decision, for debug logging. "No return on Windows"
// with a bare bool gave no way to tell OpenProcess failure, a Default token,
// or an inherited Full-parent apart — this makes that diagnosable without
// re-deriving the token checks a second time just for logging.
type elevationEvaluation struct {
	procKnown, parentKnown bool
	procType, parentType   uint32
	counted                bool
}

func evaluateUACElevation(pid, parentPID uint32) elevationEvaluation {
	procType, procKnown := getTokenElevationType(pid)
	var parentType uint32
	var parentKnown bool
	if parentPID != 0 {
		parentType, parentKnown = getTokenElevationType(parentPID)
	}
	counted := procKnown && shouldCountElevation(procType, parentType, parentKnown)
	return elevationEvaluation{procKnown, parentKnown, procType, parentType, counted}
}

// isUACElevatedLaunch reports whether a just-started process represents a
// user-driven UAC elevation (split-token Full, not inherited from an already
// elevated parent).
func isUACElevatedLaunch(pid, parentPID uint32) bool {
	return evaluateUACElevation(pid, parentPID).counted
}
