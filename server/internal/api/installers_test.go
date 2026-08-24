package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractVersion(t *testing.T) {
	tests := []struct {
		name string
		want []int
	}{
		{"openlabstats-agent-0.1.10.msi", []int{0, 1, 10}},
		{"openlabstats-agent-0.1.9.msi", []int{0, 1, 9}},
		{"openlabstats-agent-0.1.10-universal.pkg", []int{0, 1, 10}},
		{"openlabstats-agent-0.1.9-arm64.pkg", []int{0, 1, 9}},
		{"openlabstats-agent.msi", nil},
		{"", nil},
	}
	for _, tt := range tests {
		got := extractVersion(tt.name)
		if len(got) != len(tt.want) {
			t.Errorf("extractVersion(%q) = %v, want %v", tt.name, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("extractVersion(%q) = %v, want %v", tt.name, got, tt.want)
				break
			}
		}
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b []int
		want int // sign
	}{
		// The regression that shipped 0.1.9 as "latest" over 0.1.10.
		{[]int{0, 1, 10}, []int{0, 1, 9}, 1},
		{[]int{0, 1, 9}, []int{0, 1, 10}, -1},
		{[]int{0, 1, 10}, []int{0, 1, 10}, 0},
		{[]int{0, 2, 0}, []int{0, 1, 99}, 1},
		{[]int{1, 0}, []int{0, 9, 9}, 1},
		// Missing trailing components count as zero.
		{[]int{0, 1}, []int{0, 1, 0}, 0},
		{[]int{0, 1, 1}, []int{0, 1}, 1},
		// Unversioned sorts below anything versioned.
		{nil, []int{0, 0, 1}, -1},
		{nil, nil, 0},
	}
	for _, tt := range tests {
		got := compareVersions(tt.a, tt.b)
		if (got > 0) != (tt.want > 0) || (got < 0) != (tt.want < 0) {
			t.Errorf("compareVersions(%v, %v) = %d, want sign %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// TestFindLatestInstallerPicksNewestVersion reproduces the production layout on
// openstats.colgate.edu, where 0.1.10 and 0.1.9 both existed but the endpoint
// served 0.1.9 — leaving Windows agents unable to move off 0.1.9.
func TestFindLatestInstallerPicksNewestVersion(t *testing.T) {
	publicDir := t.TempDir()
	installers := filepath.Join(publicDir, "installers")
	if err := os.MkdirAll(installers, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"openlabstats-agent-0.1.8.msi",
		"openlabstats-agent-0.1.9.msi",
		"openlabstats-agent-0.1.10.msi",
		"openlabstats-agent-0.1.9-universal.pkg",
		"openlabstats-agent-0.1.10-universal.pkg",
	} {
		if err := os.WriteFile(filepath.Join(installers, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		osVersion string
		want      string
	}{
		{"Microsoft Windows 11 Education", "openlabstats-agent-0.1.10.msi"},
		{"macOS 26.5.1", "openlabstats-agent-0.1.10-universal.pkg"},
		// Bare numeric version is treated as macOS.
		{"14.3.1", "openlabstats-agent-0.1.10-universal.pkg"},
		// Unknown OS falls back to the MSI.
		{"", "openlabstats-agent-0.1.10.msi"},
	}
	for _, tt := range tests {
		got, err := findLatestInstaller(publicDir, tt.osVersion)
		if err != nil {
			t.Errorf("findLatestInstaller(%q) error: %v", tt.osVersion, err)
			continue
		}
		if got != tt.want {
			t.Errorf("findLatestInstaller(%q) = %q, want %q", tt.osVersion, got, tt.want)
		}
	}
}

func TestNormalizeServerAddress(t *testing.T) {
	tests := []struct{ in, want string }{
		{"openstats.colgate.edu", "https://openstats.colgate.edu"},
		{"https://openstats.colgate.edu", "https://openstats.colgate.edu"},
		{"https://openstats.colgate.edu/", "https://openstats.colgate.edu"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"  openstats.colgate.edu  ", "https://openstats.colgate.edu"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := normalizeServerAddress(tt.in); got != tt.want {
			t.Errorf("normalizeServerAddress(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A platform with no matching installer must fall back to the other extension
// rather than returning nothing.
func TestFindLatestInstallerFallsBackAcrossPlatforms(t *testing.T) {
	publicDir := t.TempDir()
	installers := filepath.Join(publicDir, "installers")
	if err := os.MkdirAll(installers, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installers, "openlabstats-agent-0.1.10.msi"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findLatestInstaller(publicDir, "macOS 26.5.1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "openlabstats-agent-0.1.10.msi" {
		t.Errorf("got %q, want the .msi fallback", got)
	}
}
