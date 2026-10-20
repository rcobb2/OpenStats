package normalizer

import "testing"

func TestIsLocalDrivePath(t *testing.T) {
	tests := []struct {
		in   string
		root string
		ok   bool
	}{
		{`C:\Windows\x.exe`, `C:\`, true},
		{`d:\a\b.exe`, `d:\`, true},
		{`\\?\C:\Program Files\x.exe`, `C:\`, true},
		{`C:/Windows/x.exe`, `C:\`, true},
		{`\\host\share\x.exe`, "", false},
		{`\\host@80\x.exe`, "", false},
		{`\\?\UNC\host\share\x.exe`, "", false},
		{`\\.\PhysicalDrive0`, "", false},
		{`\\.\C:\x.exe`, "", false},
		{`//host/share/x.exe`, "", false},
		{`x.exe`, "", false},
		{`\Windows\x.exe`, "", false},
		{`C:x.exe`, "", false},
		{`C:`, "", false},
		{``, "", false},
		{"C:\\a\x00b.exe", "", false},
		{`1:\x.exe`, "", false},
	}
	for _, tc := range tests {
		root, ok := isLocalDrivePath(tc.in)
		if ok != tc.ok || root != tc.root {
			t.Errorf("isLocalDrivePath(%q) = (%q,%v), want (%q,%v)", tc.in, root, ok, tc.root, tc.ok)
		}
	}
}
