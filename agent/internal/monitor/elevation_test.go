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

func TestShouldCountRootLaunch(t *testing.T) {
	tests := []struct {
		name        string
		childUID    uint32
		parentUID   uint32
		parentKnown bool
		want        bool
	}{
		{"root child, non-root parent (sudo)", 0, 501, true, true},
		{"root child, root parent (daemon's own child)", 0, 0, true, false},
		{"root child, parent unknown (already exited: no one to attribute to)", 0, 0, false, false},
		{"non-root child, non-root parent", 501, 501, true, false},
		{"non-root child, root parent (ordinary fork from a daemon)", 501, 0, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldCountRootLaunch(tt.childUID, tt.parentUID, tt.parentKnown); got != tt.want {
				t.Errorf("shouldCountRootLaunch(%d, %d, %v) = %v, want %v",
					tt.childUID, tt.parentUID, tt.parentKnown, got, tt.want)
			}
		})
	}
}
