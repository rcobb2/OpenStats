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

// isUACElevatedLaunch reports whether a just-started process represents a
// user-driven UAC elevation (split-token Full, not inherited from an already
// elevated parent).
func isUACElevatedLaunch(pid, parentPID uint32) bool {
	procType, ok := getTokenElevationType(pid)
	if !ok {
		return false
	}
	var parentType uint32
	parentKnown := false
	if parentPID != 0 {
		parentType, parentKnown = getTokenElevationType(parentPID)
	}
	return shouldCountElevation(procType, parentType, parentKnown)
}
