//go:build windows

package monitor

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// getTokenElevationType returns the TOKEN_ELEVATION_TYPE for pid. ok=false when
// the process is already gone or its token is inaccessible (short-lived process
// race, protected/PPL process) — callers treat that as "unknown", never as an
// error worth surfacing. failStep/failErr identify which Win32 call failed and
// why, for debug logging only — "no return on Windows" with a bare bool gave
// no way to tell an access-denied OpenProcess apart from a process that had
// already exited by the time we checked.
//
// Uses PROCESS_QUERY_INFORMATION rather than the newer
// PROCESS_QUERY_LIMITED_INFORMATION: the latter is documented as sufficient
// for basic info (exit code, image name) but real-hardware testing showed
// OpenProcessToken failing on ordinary, still-running processes with it —
// token operations need the fuller access right.
func getTokenElevationType(pid uint32) (elevType uint32, ok bool, failStep string, failErr error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, pid)
	if err != nil {
		return 0, false, "OpenProcess", err
	}
	defer windows.CloseHandle(h)

	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return 0, false, "OpenProcessToken", err
	}
	defer tok.Close()

	var ret uint32
	err = windows.GetTokenInformation(tok, windows.TokenElevationType,
		(*byte)(unsafe.Pointer(&elevType)), uint32(unsafe.Sizeof(elevType)), &ret)
	if err != nil {
		return 0, false, "GetTokenInformation", err
	}
	return elevType, true, "", nil
}

// elevationEvaluation carries the intermediate token-check results behind an
// isUACElevatedLaunch decision, for debug logging.
type elevationEvaluation struct {
	procKnown, parentKnown bool
	procType, parentType   uint32
	counted                bool
	// procFailStep/procFailErr are set only when procKnown is false, naming
	// which Win32 call failed on the target process itself (the parent's
	// failure detail isn't tracked — its bare unknown/known state is enough
	// for the counting decision).
	procFailStep string
	procFailErr  error
}

func evaluateUACElevation(pid, parentPID uint32) elevationEvaluation {
	procType, procKnown, failStep, failErr := getTokenElevationType(pid)
	var parentType uint32
	var parentKnown bool
	if parentPID != 0 {
		parentType, parentKnown, _, _ = getTokenElevationType(parentPID)
	}
	counted := procKnown && shouldCountElevation(procType, parentType, parentKnown)
	return elevationEvaluation{
		procKnown: procKnown, parentKnown: parentKnown,
		procType: procType, parentType: parentType,
		counted:      counted,
		procFailStep: failStep, procFailErr: failErr,
	}
}

// isUACElevatedLaunch reports whether a just-started process represents a
// user-driven UAC elevation (split-token Full, not inherited from an already
// elevated parent).
func isUACElevatedLaunch(pid, parentPID uint32) bool {
	return evaluateUACElevation(pid, parentPID).counted
}
