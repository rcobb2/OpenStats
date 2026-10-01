package normalizer

import "testing"

// NTFS preserves whatever case an installer wrote for the extension, so an
// exe that falls through to this last-resort fallback (no mapping, no
// resolvable PE/plist metadata) must still get a clean display name
// regardless of how ".exe"/".EXE"/mixed-case is spelled.
func TestCleanExeName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"notepad.exe", "Notepad"},
		{"NOTEPAD.EXE", "NOTEPAD"},
		{"Launcher.Exe", "Launcher"},
		{"sVcHoSt.ExE", "SVcHoSt"},
		{"noextension", "Noextension"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := cleanExeName(tt.in); got != tt.want {
			t.Errorf("cleanExeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
