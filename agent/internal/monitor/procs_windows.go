//go:build windows

package monitor

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// RunningPIDs enumerates every running PID with a Toolhelp32 process snapshot.
// It returns an error (never an empty map) if enumeration fails.
func RunningPIDs() (map[uint32]bool, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return nil, fmt.Errorf("Process32First: %w", err)
	}
	pids := make(map[uint32]bool, 256)
	for {
		pids[pe.ProcessID] = true
		if err := windows.Process32Next(snap, &pe); err != nil {
			if err == windows.ERROR_NO_MORE_FILES {
				break
			}
			return nil, fmt.Errorf("Process32Next: %w", err)
		}
	}
	return pids, nil
}
