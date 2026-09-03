package monitor

import "testing"

// fakeWindowsProcTree builds a windowsAncestorLookup from a map of
// pid -> (elevationType, ppid), so tests can construct arbitrary process
// chains without any real Win32 calls.
func fakeWindowsProcTree(procs map[uint32][2]uint32) windowsAncestorLookup {
	return func(pid uint32) (elevType, ppid uint32, ok bool) {
		p, exists := procs[pid]
		if !exists {
			return 0, 0, false
		}
		return p[0], p[1], true
	}
}

func TestFindElevationBoundary(t *testing.T) {
	tests := []struct {
		name              string
		startPID          uint32
		procs             map[uint32][2]uint32 // pid -> {elevationType, ppid}
		wantFound         bool
		wantChainReadable bool
	}{
		{
			name:      "immediate non-Full parent (plain UAC consent, no indirection)",
			startPID:  100,
			procs:     map[uint32][2]uint32{100: {tokenElevationTypeLimited, 50}},
			wantFound: true, wantChainReadable: true,
		},
		{
			name:     "Full parent whose own parent is non-Full (conpty/terminal indirection)",
			startPID: 100, // the elevated process's ppid
			procs: map[uint32][2]uint32{
				100: {tokenElevationTypeFull, 90},    // e.g. a terminal-hosting process: also Full
				90:  {tokenElevationTypeLimited, 50}, // the true non-elevated origin
			},
			wantFound: true, wantChainReadable: true,
		},
		{
			name:     "Full all the way to the top: ordinary token inheritance, not new",
			startPID: 100,
			procs: map[uint32][2]uint32{
				100: {tokenElevationTypeFull, 1},
				1:   {tokenElevationTypeFull, 0},
			},
			wantFound: false, wantChainReadable: true,
		},
		{
			name:      "unreadable immediate parent",
			startPID:  100,
			procs:     map[uint32][2]uint32{},
			wantFound: false, wantChainReadable: false,
		},
		{
			name:     "unreadable ancestor mid-chain",
			startPID: 100,
			procs: map[uint32][2]uint32{
				100: {tokenElevationTypeFull, 90},
				// 90 not in the map: lookup(90) fails
			},
			wantFound: false, wantChainReadable: false,
		},
		{
			name:     "self-referential ppid does not loop forever",
			startPID: 100,
			procs: map[uint32][2]uint32{
				100: {tokenElevationTypeFull, 100},
			},
			wantFound: false, wantChainReadable: true,
		},
		{
			name:     "hop count is bounded",
			startPID: 1,
			procs: func() map[uint32][2]uint32 {
				m := make(map[uint32][2]uint32)
				for i := uint32(1); i <= maxAncestorHops+2; i++ {
					m[i] = [2]uint32{tokenElevationTypeFull, i + 1}
				}
				return m
			}(),
			wantFound: false, wantChainReadable: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found, chainReadable := findElevationBoundary(tt.startPID, fakeWindowsProcTree(tt.procs))
			if found != tt.wantFound {
				t.Errorf("found = %v, want %v", found, tt.wantFound)
			}
			if chainReadable != tt.wantChainReadable {
				t.Errorf("chainReadable = %v, want %v", chainReadable, tt.wantChainReadable)
			}
		})
	}
}

// fakeProcTree builds a procAncestorLookup from a map of pid -> (uid, ppid),
// so tests can construct arbitrary process chains without any real OS calls.
func fakeProcTree(procs map[uint32][2]uint32) procAncestorLookup {
	return func(pid uint32) (uid, ppid uint32, ok bool) {
		p, exists := procs[pid]
		if !exists {
			return 0, 0, false
		}
		return p[0], p[1], true
	}
}

func TestIsIncidentalSetuidTool(t *testing.T) {
	// login moved here after real production data showed 50,000-60,000+
	// "elevations" for it — invoked automatically by macOS session
	// plumbing, not a deliberate user action, unlike su/sudo.
	for _, exe := range []string{"ps", "top", "traceroute", "traceroute6", "crontab", "login"} {
		if !isIncidentalSetuidTool(exe) {
			t.Errorf("isIncidentalSetuidTool(%q) = false, want true", exe)
		}
	}
	for _, exe := range []string{"sudo", "su", "softwareupdate", "installer", ""} {
		if isIncidentalSetuidTool(exe) {
			t.Errorf("isIncidentalSetuidTool(%q) = true, want false — this should still count as a deliberate elevation", exe)
		}
	}
}

func TestIsIncidentalConsoleHost(t *testing.T) {
	// Case-insensitive: Windows process names aren't consistently normalized
	// at the source (WMI reports whatever casing the OS gives it).
	for _, exe := range []string{"OpenConsole.exe", "openconsole.exe", "conhost.exe", "CONHOST.EXE"} {
		if !isIncidentalConsoleHost(exe) {
			t.Errorf("isIncidentalConsoleHost(%q) = false, want true", exe)
		}
	}
	for _, exe := range []string{"powershell.exe", "cmd.exe", "WindowsTerminal.exe", ""} {
		if isIncidentalConsoleHost(exe) {
			t.Errorf("isIncidentalConsoleHost(%q) = true, want false — this is a real shell, not just a pty host", exe)
		}
	}
}

func TestFindEscalatingAncestor(t *testing.T) {
	tests := []struct {
		name      string
		startPID  uint32
		procs     map[uint32][2]uint32 // pid -> {uid, ppid}
		wantUID   uint32
		wantFound bool
	}{
		{
			name:     "immediate non-root parent (plain sudo, no monitor subprocess)",
			startPID: 100,
			procs:    map[uint32][2]uint32{100: {501, 50}},
			wantUID:  501, wantFound: true,
		},
		{
			name:     "root parent whose own parent is non-root (sudo's monitor subprocess)",
			startPID: 100, // sleep's ppid
			procs: map[uint32][2]uint32{
				100: {0, 90},   // sudo's monitor: already root
				90:  {501, 50}, // the invoking shell: not root
			},
			wantUID: 501, wantFound: true,
		},
		{
			name:     "chain terminates at launchd without a non-root ancestor",
			startPID: 100,
			procs: map[uint32][2]uint32{
				100: {0, 1}, // some daemon's child
				1:   {0, 0}, // launchd: ppid 0
			},
			wantFound: false,
		},
		{
			name:      "unreadable immediate parent: no one to attribute to",
			startPID:  100,
			procs:     map[uint32][2]uint32{}, // lookup(100) fails
			wantFound: false,
		},
		{
			name:     "unreadable ancestor mid-chain",
			startPID: 100,
			procs: map[uint32][2]uint32{
				100: {0, 90}, // readable, root, points further up
				// 90 is not in the map: lookup(90) fails
			},
			wantFound: false,
		},
		{
			name:     "self-referential ppid does not loop forever",
			startPID: 100,
			procs: map[uint32][2]uint32{
				100: {0, 100}, // pathological: its own parent
			},
			wantFound: false,
		},
		{
			name:     "hop count is bounded",
			startPID: 1,
			procs: func() map[uint32][2]uint32 {
				// A chain of maxAncestorHops+2 all-root pids, never resolving
				// to a non-root ancestor within the bound.
				m := make(map[uint32][2]uint32)
				for i := uint32(1); i <= maxAncestorHops+2; i++ {
					m[i] = [2]uint32{0, i + 1}
				}
				return m
			}(),
			wantFound: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uid, found := findEscalatingAncestor(tt.startPID, fakeProcTree(tt.procs))
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}
			if found && uid != tt.wantUID {
				t.Errorf("uid = %d, want %d", uid, tt.wantUID)
			}
		})
	}
}
