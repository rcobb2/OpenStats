package enrollment

import (
	"io"
	"log/slog"
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
