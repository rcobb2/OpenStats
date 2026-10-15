//go:build windows

package session

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procGetLastInputInfo = user32.NewProc("GetLastInputInfo")
	procGetTickCount     = kernel32.NewProc("GetTickCount")
)

// lastInputInfo mirrors LASTINPUTINFO.
type lastInputInfo struct {
	CbSize uint32
	DwTime uint32
}

// ForegroundPID returns the PID owning the foreground window of the calling
// process's desktop, or 0 if there is none. Only meaningful inside a user
// session; in session 0 it always sees nothing.
func ForegroundPID() uint32 {
	hwnd := windows.GetForegroundWindow()
	if hwnd == 0 {
		return 0
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil {
		return 0
	}
	return pid
}

// IdleTime returns the time since the last keyboard/mouse input in the
// calling process's session, or 0 if it cannot be determined (treated by the
// caller as "not idle", which fails toward the old behavior).
func IdleTime() time.Duration {
	info := lastInputInfo{CbSize: uint32(unsafe.Sizeof(lastInputInfo{}))}
	if r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0
	}
	now, _, _ := procGetTickCount.Call()
	// Both values are 32-bit millisecond tick counts that wrap every ~49.7
	// days; unsigned subtraction yields the right delta across a wrap.
	return time.Duration(uint32(now)-info.DwTime) * time.Millisecond
}
