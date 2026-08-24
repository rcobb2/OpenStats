// Package urlutil normalizes admin-supplied server addresses.
//
// It lives in its own package because both internal/config (applying the
// installer's registry values) and internal/enrollment (building request URLs)
// need it, and neither should depend on the other.
package urlutil

import (
	neturl "net/url"
	"strings"
)

// NormalizeServerURL turns an admin-supplied server address into a usable base
// URL. Two shapes used to break the agent silently:
//
//   - A bare hostname ("openstats.colgate.edu") — which the installer UI
//     suggested, and which an SCCM package duly passed as SERVERADDRESS to the
//     whole Academic fleet. Every request then fails with `unsupported protocol
//     scheme ""`, and the update-host trust check sees an empty hostname, so the
//     agent neither registers nor self-updates while the install reports success.
//   - A trailing slash — paths become "//api/v1/...".
//
// A scheme-less address is assumed to be https; write the scheme explicitly for
// plain-HTTP servers (e.g. "http://localhost:8080").
//
// Normalizing is idempotent, so it is safe to apply at more than one layer.
func NormalizeServerURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := neturl.Parse(s)
	if err != nil || u.Host == "" {
		return strings.TrimRight(s, "/")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
