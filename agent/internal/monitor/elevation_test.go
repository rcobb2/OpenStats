package monitor

import "testing"

func TestShouldCountElevation(t *testing.T) {
	tests := []struct {
		name        string
		procType    uint32
		parentType  uint32
		parentKnown bool
		want        bool
	}{
		{"full token, limited parent (UAC consent)", tokenElevationTypeFull, tokenElevationTypeLimited, true, true},
		{"full token, default parent (launched by service)", tokenElevationTypeFull, tokenElevationTypeDefault, true, true},
		{"full token inherited from full parent", tokenElevationTypeFull, tokenElevationTypeFull, true, false},
		{"full token, parent unknown (favor counting)", tokenElevationTypeFull, 0, false, true},
		{"default token (built-in admin / UAC off)", tokenElevationTypeDefault, tokenElevationTypeLimited, true, false},
		{"limited token (ordinary user)", tokenElevationTypeLimited, tokenElevationTypeLimited, true, false},
		{"limited token, parent unknown", tokenElevationTypeLimited, 0, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldCountElevation(tt.procType, tt.parentType, tt.parentKnown); got != tt.want {
				t.Errorf("shouldCountElevation(%d, %d, %v) = %v, want %v",
					tt.procType, tt.parentType, tt.parentKnown, got, tt.want)
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
	for _, exe := range []string{"ps", "top", "traceroute", "traceroute6", "crontab"} {
		if !isIncidentalSetuidTool(exe) {
			t.Errorf("isIncidentalSetuidTool(%q) = false, want true", exe)
		}
	}
	for _, exe := range []string{"sudo", "su", "login", "softwareupdate", "installer", ""} {
		if isIncidentalSetuidTool(exe) {
			t.Errorf("isIncidentalSetuidTool(%q) = true, want false — this should still count as a deliberate elevation", exe)
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
