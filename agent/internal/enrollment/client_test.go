package enrollment

import (
	"io"
	"log/slog"
	neturl "net/url"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNormalizeServerURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// The shape the installer UI's placeholder suggested. Without a scheme
		// every request fails with `unsupported protocol scheme ""`.
		{"bare hostname", "openstats.colgate.edu", "https://openstats.colgate.edu"},
		{"bare host with port", "openstats.colgate.edu:8080", "https://openstats.colgate.edu:8080"},
		{"already https", "https://openstats.colgate.edu", "https://openstats.colgate.edu"},
		{"trailing slash", "https://openstats.colgate.edu/", "https://openstats.colgate.edu"},
		{"several trailing slashes", "https://openstats.colgate.edu///", "https://openstats.colgate.edu"},
		// Plain HTTP must survive — don't force https on a local dev server.
		{"explicit http localhost", "http://localhost:8080", "http://localhost:8080"},
		{"explicit http trailing slash", "http://localhost:8080/", "http://localhost:8080"},
		{"surrounding whitespace", "  https://openstats.colgate.edu  ", "https://openstats.colgate.edu"},
		{"empty stays empty", "", ""},
		{"whitespace only stays empty", "   ", ""},
		{"subpath preserved", "https://host.edu/openstats", "https://host.edu/openstats"},
		{"subpath trailing slash", "https://host.edu/openstats/", "https://host.edu/openstats"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeServerURL(tt.in); got != tt.want {
				t.Errorf("NormalizeServerURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// The registration URL the agent builds must be absolute and single-slashed for
// every accepted input shape.
func TestNormalizedURLBuildsValidRegisterEndpoint(t *testing.T) {
	for _, in := range []string{
		"openstats.colgate.edu",
		"https://openstats.colgate.edu",
		"https://openstats.colgate.edu/",
		"http://localhost:8080/",
	} {
		base := NormalizeServerURL(in)
		full := base + "/api/v1/agents/register"
		u, err := neturl.Parse(full)
		if err != nil {
			t.Errorf("input %q produced unparseable URL %q: %v", in, full, err)
			continue
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			t.Errorf("input %q produced non-absolute URL %q (scheme %q)", in, full, u.Scheme)
		}
		if u.Host == "" {
			t.Errorf("input %q produced URL with no host: %q", in, full)
		}
		if u.Path != "/api/v1/agents/register" {
			t.Errorf("input %q produced path %q, want /api/v1/agents/register", in, u.Path)
		}
	}
}

// A scheme-less address used to make isTrustedUpdateHost compare against an
// empty hostname, so every server-directed self-update was rejected.
func TestTrustedUpdateHostWorksForSchemelessConfig(t *testing.T) {
	c := NewClient("openstats.colgate.edu", 9183, "", "", testLogger())
	if !c.isTrustedUpdateHost("openstats.colgate.edu") {
		t.Errorf("expected openstats.colgate.edu to be trusted, serverURL=%q", c.serverURL)
	}
	if c.isTrustedUpdateHost("evil.example.com") {
		t.Error("unrelated host must not be trusted")
	}
}
