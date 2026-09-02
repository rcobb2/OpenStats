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
