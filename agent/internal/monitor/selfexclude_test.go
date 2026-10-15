package monitor

import "testing"

func TestMatchesAgent(t *testing.T) {
	self := "/opt/openlabstats/openlabstats-agent"
	winSelf := `C:\Program Files\OpenLabStats\openlabstats-agent.exe`
	tests := []struct {
		name     string
		exeName  string
		exePath  string
		self     string
		foldCase bool
		want     bool
	}{
		{"same path", "openlabstats-agent", self, self, false, true},
		{"renamed binary, same path", "other", self, self, false, true},
		{"path unreadable, base name", "openlabstats-agent", "", self, false, true},
		{"exe base name", "openlabstats-agent.exe", "", "", true, true},
		{"windows case-insensitive path", "X.EXE", `c:\program files\openlabstats\OPENLABSTATS-AGENT.EXE`, winSelf, true, true},
		{"unix is case-sensitive on path", "x", "/OPT/openlabstats/openlabstats-agent", self, false, false},
		{"other app", "firefox", "/Applications/Firefox.app/firefox", self, false, false},
		{"similar name", "openlabstats-agent-helper", "/tmp/x", self, false, false},
		{"no self known", "firefox", "/a/b", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesAgent(tt.exeName, tt.exePath, tt.self, tt.foldCase); got != tt.want {
				t.Errorf("matchesAgent(%q, %q) = %v, want %v", tt.exeName, tt.exePath, got, tt.want)
			}
		})
	}
}
