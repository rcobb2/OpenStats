package config

import "testing"

func TestParsePort(t *testing.T) {
	tests := []struct {
		in   string
		want int
		ok   bool
	}{
		{"9183", 9183, true},
		{"1", 1, true},
		{"65535", 65535, true},
		{"  9183  ", 9183, true},
		// An unset MSI property formats to "" — must not become port 0.
		{"", 0, false},
		{"0", 0, false},
		{"65536", 0, false},
		{"-1", 0, false},
		{"[PORT]", 0, false},
		{"abc", 0, false},
	}
	for _, tt := range tests {
		got, ok := parsePort(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("parsePort(%q) = (%d, %v), want (%d, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
