package monitor

import (
	"context"
	"log/slog"
	"syscall"
	"time"
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

// RunForegroundPoller polls the foreground window every interval and attributes
// active time to the corresponding process group in the Tracker.
func RunForegroundPoller(ctx context.Context, tracker *Tracker, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logger.Info("started foreground window poller", "interval", interval)

	for {
		select {
		case <-ctx.Done():
			logger.Info("Foreground window poller shutting down")
			return
		case <-ticker.C:
			pid := getForegroundPID()
			if pid != 0 {
				logger.Debug("foreground window PID", "pid", pid)
				tracker.IncrementForeground(pid, interval)
			} else {
				logger.Debug("no foreground window active")
			}
		}
	}
}
