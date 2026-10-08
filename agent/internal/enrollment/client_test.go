package enrollment

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The exhaustive normalization table lives in internal/urlutil. This just
// confirms enrollment's entry point is actually wired to it.
func TestNormalizeServerURLDelegates(t *testing.T) {
	if got := NormalizeServerURL("openstats.colgate.edu"); got != "https://openstats.colgate.edu" {
		t.Errorf("NormalizeServerURL did not normalize: got %q", got)
	}
	if got := NormalizeServerURL(""); got != "" {
		t.Errorf("empty input should stay empty, got %q", got)
	}
}

// NewClient must normalize, or every request built from serverURL fails with
// `unsupported protocol scheme ""`.
func TestNewClientNormalizesServerURL(t *testing.T) {
	c := NewClient("openstats.colgate.edu/", 9183, "", "", testLogger())
	if c.serverURL != "https://openstats.colgate.edu" {
		t.Errorf("NewClient stored %q, want https://openstats.colgate.edu", c.serverURL)
	}
}

// A scheme-less address used to make isTrustedUpdateHost compare against an
// empty hostname, so every server-directed self-update was silently rejected.
func TestTrustedUpdateHostWorksForSchemelessConfig(t *testing.T) {
	c := NewClient("openstats.colgate.edu", 9183, "", "", testLogger())
	if !c.isTrustedUpdateHost("openstats.colgate.edu") {
		t.Errorf("expected openstats.colgate.edu to be trusted, serverURL=%q", c.serverURL)
	}
	if c.isTrustedUpdateHost("evil.example.com") {
		t.Error("unrelated host must not be trusted")
	}
}

func TestVerifyDownloadChecksumEmptyExpectedAlwaysPasses(t *testing.T) {
	path := t.TempDir() + "/file.bin"
	if err := os.WriteFile(path, []byte("anything"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyDownloadChecksum(path, ""); err != nil {
		t.Errorf("expected nil error for empty expected checksum, got %v", err)
	}
}

func TestVerifyDownloadChecksumMatches(t *testing.T) {
	content := []byte("totally real msi bytes")
	path := t.TempDir() + "/file.bin"
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	expected := hex.EncodeToString(sum[:])

	if err := verifyDownloadChecksum(path, expected); err != nil {
		t.Errorf("expected nil error for matching checksum, got %v", err)
	}
	// Case-insensitive comparison (server hex encoding should always be
	// lowercase, but don't be brittle about it).
	if err := verifyDownloadChecksum(path, strings.ToUpper(expected)); err != nil {
		t.Errorf("expected case-insensitive match to pass, got %v", err)
	}
}

// Regression: RunHeartbeat used to call IsInMaintenanceWindow directly, with
// no way for setmaintenance's override to ever reach this decision — the CLI
// command printed a convincing "forced on/off" message while the running
// service kept gating self-updates purely on the server's time-based window.
// inMaintenanceWindow must let a persisted override win outright in both
// directions, and fall through to the time-based window when none is set.
func TestInMaintenanceWindowOverrideWinsOverSchedule(t *testing.T) {
	dir := t.TempDir()
	c := NewClient("openstats.colgate.edu", 9183, "", "", testLogger()).WithMaintenanceOverrideDir(dir)

	// Outside any configured window by the time-based check (start==end means
	// never), so a nil override must say "not in window".
	neverSettings := &SystemSettings{MaintenanceWindowStart: "10:00", MaintenanceWindowEnd: "10:00"}
	if c.inMaintenanceWindow(neverSettings) {
		t.Fatal("expected false with no override and a zero-length window")
	}

	on := true
	if err := WriteMaintenanceOverride(dir, &on); err != nil {
		t.Fatal(err)
	}
	if !c.inMaintenanceWindow(neverSettings) {
		t.Error("forced-on override should win even when the schedule says never")
	}

	off := false
	if err := WriteMaintenanceOverride(dir, &off); err != nil {
		t.Fatal(err)
	}
	alwaysSettings := &SystemSettings{} // empty start/end means "always in window"
	if c.inMaintenanceWindow(alwaysSettings) {
		t.Error("forced-off override should win even when the schedule says always")
	}
}

func TestVerifyDownloadChecksumMismatchErrors(t *testing.T) {
	path := t.TempDir() + "/file.bin"
	if err := os.WriteFile(path, []byte("real content"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := verifyDownloadChecksum(path, "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected an error for a checksum mismatch")
	}
}
