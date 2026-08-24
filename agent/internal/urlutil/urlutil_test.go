package urlutil

import (
	neturl "net/url"
	"testing"
)

func TestNormalizeServerURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// The literal value the Academic SCCM package passed as SERVERADDRESS.
		// Installs reported success; not one machine registered.
		{"sccm bare hostname", "openstats.colgate.edu", "https://openstats.colgate.edu"},
		{"bare host with port", "openstats.colgate.edu:8080", "https://openstats.colgate.edu:8080"},
		{"already https", "https://openstats.colgate.edu", "https://openstats.colgate.edu"},
		{"trailing slash", "https://openstats.colgate.edu/", "https://openstats.colgate.edu"},
		{"several trailing slashes", "https://openstats.colgate.edu///", "https://openstats.colgate.edu"},
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

// The function is applied at both the config and enrollment layers, so it must
// be safe to run more than once.
func TestNormalizeServerURLIsIdempotent(t *testing.T) {
	for _, in := range []string{
		"openstats.colgate.edu",
		"https://openstats.colgate.edu/",
		"http://localhost:8080/",
		"",
	} {
		once := NormalizeServerURL(in)
		twice := NormalizeServerURL(once)
		if once != twice {
			t.Errorf("not idempotent for %q: once=%q twice=%q", in, once, twice)
		}
	}
}

func TestNormalizedURLBuildsValidEndpoint(t *testing.T) {
	for _, in := range []string{
		"openstats.colgate.edu",
		"https://openstats.colgate.edu",
		"https://openstats.colgate.edu/",
		"http://localhost:8080/",
	} {
		full := NormalizeServerURL(in) + "/api/v1/agents/register"
		u, err := neturl.Parse(full)
		if err != nil {
			t.Errorf("input %q produced unparseable URL %q: %v", in, full, err)
			continue
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			t.Errorf("input %q produced non-absolute URL %q", in, full)
		}
		if u.Host == "" {
			t.Errorf("input %q produced URL with no host: %q", in, full)
		}
		if u.Path != "/api/v1/agents/register" {
			t.Errorf("input %q produced path %q", in, u.Path)
		}
	}
}
