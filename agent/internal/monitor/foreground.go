//go:build windows

package monitor

import (
	"syscall"
	"unsafe"
)

var (
	user32                       = syscall.MustLoadDLL("user32.dll")
	procGetForegroundWindow      = user32.MustFindProc("GetForegroundWindow")
	procGetWindowThreadProcessId = user32.MustFindProc("GetWindowThreadProcessId")
)

// getForegroundPID returns the process ID of the currently active foreground window.
// It returns 0 if no window is active or an error occurs.
func getForegroundPID() uint32 {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return 0
	}

	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}

