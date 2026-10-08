//go:build darwin

package inventory

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const testPlistXML = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>com.example.testapp</string>
	<key>CFBundleDisplayName</key>
	<string>Test App</string>
	<key>CFBundleShortVersionString</key>
	<string>1.2.3</string>
</dict>
</plist>
`

func writeTestPlist(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Info.plist")
	if err := os.WriteFile(path, []byte(testPlistXML), 0644); err != nil {
		t.Fatalf("failed to write test plist: %v", err)
	}
	return path
}

func TestReadInfoPlistXML(t *testing.T) {
	path := writeTestPlist(t)

	info, err := readInfoPlist(path)
	if err != nil {
		t.Fatalf("readInfoPlist failed on XML plist: %v", err)
	}
	if info["CFBundleIdentifier"] != "com.example.testapp" {
		t.Errorf("CFBundleIdentifier = %q, want %q", info["CFBundleIdentifier"], "com.example.testapp")
	}
	if info["CFBundleShortVersionString"] != "1.2.3" {
		t.Errorf("CFBundleShortVersionString = %q, want %q", info["CFBundleShortVersionString"], "1.2.3")
	}
}

// Regression test for the actual bug this file was written to fix: Xcode's
// standard Release/Archive output format is binary, not XML, and the
// previous version of readInfoPlist explicitly rejected it — silently
// dropping the whole .app bundle from inventory. plutil (present on every
// real macOS machine, including this test's CI runner) converts an XML
// fixture to real binary plist format in place, so this exercises the exact
// bplist00 parsing path via plutil, not just a round-trip of our own XML.
func TestReadInfoPlistBinary(t *testing.T) {
	if _, err := exec.LookPath("plutil"); err != nil {
		t.Skip("plutil not available on this system")
	}

	path := writeTestPlist(t)
	if err := exec.Command("plutil", "-convert", "binary1", path).Run(); err != nil {
		t.Fatalf("failed to convert test fixture to binary plist: %v", err)
	}

	// Confirm the fixture really is binary now, not accidentally still XML
	// (which would make this test pass without ever exercising the binary
	// path it's meant to cover).
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read converted fixture: %v", err)
	}
	if len(raw) < 6 || string(raw[:6]) != "bplist" {
		t.Fatalf("test fixture is not binary plist after conversion (got %q)", raw[:min(6, len(raw))])
	}

	info, err := readInfoPlist(path)
	if err != nil {
		t.Fatalf("readInfoPlist failed on binary plist: %v", err)
	}
	if info["CFBundleIdentifier"] != "com.example.testapp" {
		t.Errorf("CFBundleIdentifier = %q, want %q", info["CFBundleIdentifier"], "com.example.testapp")
	}
	if info["CFBundleShortVersionString"] != "1.2.3" {
		t.Errorf("CFBundleShortVersionString = %q, want %q", info["CFBundleShortVersionString"], "1.2.3")
	}
}
