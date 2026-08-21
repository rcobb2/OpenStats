// Package userid resolves raw OS usernames into a single canonical identity and
// decides which accounts to ignore entirely.
//
// The same rules exist in the agent (agent/internal/userid) so that a username
// canonicalized at collection time and one canonicalized at report time agree.
// The two copies are deliberate — agent and server are separate Go modules.
// Keep them in sync when changing Canonicalize or MatchGlob.
package userid

import (
	"strings"
)

// DefaultIgnorePatterns are accounts that are never real lab users. They are
// applied on top of whatever an admin configures, so a fresh install already
// excludes OS and service accounts. Patterns are matched case-insensitively
// against both the raw username and its canonical form, and a leading
// "DOMAIN\" is ignored during matching (see Policy.Resolve).
var DefaultIgnorePatterns = []string{
	// Windows built-ins and service accounts.
	"system",
	"localsystem",
	"local system",
	"local service",
	"localservice",
	"network service",
	"networkservice",
	"anonymous logon",
	"trustedinstaller",
	"window manager",
	"font driver host",
	"usermode font driver",
	"dwm",
	"umfd",
	"umfd-*",
	"iusr",
	"iwam",
	// Common daemon accounts.
	"mssqlserver",
	"postgres",
	"mysql",
	"service",
	// macOS/Unix system accounts.
	"root",
	"daemon",
	"nobody",
	"wheel",
	"lp",
	"sshd",
	"postfix",
	"www",
	"eppc",
	"qtss",
	"cyrus",
	"vpn",
	"tokend",
	"securityagent",
	"apple_remote_desktop",
	"windowserver",
	"spotlight",
	"netinfo",
	"installer",
	"ard",
	"panopto_upload",
}

// Rule is one admin-configured user rule. Pattern is matched against the raw
// username (and its canonical form) case-insensitively and may contain "*"
// wildcards. When Ignored is set the account is dropped entirely; otherwise
// Canonical (if non-empty) replaces the derived canonical username, which is
// how a macOS shortname is merged with its domain account.
type Rule struct {
	Pattern   string
	Canonical string
	Ignored   bool
}

// Policy is the resolved set of user-tracking rules. A zero Policy still
// applies StripDomain=false; use NewPolicy for the intended defaults.
type Policy struct {
	// StripDomain removes a "DOMAIN\" prefix and an "@domain" suffix so that
	// COLGATE\jdoe (Windows) and jdoe (macOS) resolve to the same identity.
	StripDomain bool
	// Rules are admin-configured, evaluated in order; the first match wins.
	Rules []Rule
	// ExtraIgnored are ignore-only patterns beyond DefaultIgnorePatterns.
	// Rules with Ignored=true are folded in here by NewPolicy callers.
	ExtraIgnored []string
}

// NewPolicy returns a policy with domain stripping enabled and no admin rules.
func NewPolicy() *Policy {
	return &Policy{StripDomain: true}
}

// Canonicalize reduces a raw OS username to its comparable form: no domain
// prefix, no UPN suffix, lowercased. Case is always folded — Windows reports
// "COLGATE\JDoe" where macOS reports "jdoe".
func Canonicalize(raw string, stripDomain bool) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if stripDomain {
		// DOMAIN\user or DOMAIN/user — keep only the account name.
		if i := strings.LastIndexAny(s, `\/`); i >= 0 {
			s = s[i+1:]
		}
		// user@domain.edu (UPN) — keep only the account name. The guard on i>0
		// leaves an account that legitimately starts with "@" alone.
		if i := strings.Index(s, "@"); i > 0 {
			s = s[:i]
		}
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// Resolve returns the canonical username for a raw OS username and whether the
// account should be ignored. An empty or structurally invalid username is
// always ignored.
func (p *Policy) Resolve(raw string) (canonical string, ignored bool) {
	rawLower := strings.ToLower(strings.TrimSpace(raw))
	if rawLower == "" {
		return "", true
	}

	canonical = Canonicalize(raw, p.StripDomain)
	if canonical == "" {
		return "", true
	}

	// Admin rules first so an explicit rule can rescue an account that the
	// built-in heuristics would otherwise drop.
	for _, rule := range p.Rules {
		if !matchesEither(rule.Pattern, rawLower, canonical) {
			continue
		}
		if rule.Ignored {
			return canonical, true
		}
		if c := Canonicalize(rule.Canonical, p.StripDomain); c != "" {
			canonical = c
		}
		return canonical, false
	}

	// The structural check runs against the raw name as well: stripping the
	// domain from "NT SERVICE\WdiServiceHost" would otherwise hide what it is.
	if !plausibleUsername(canonical) || !plausibleUsername(rawLower) {
		return canonical, true
	}
	for _, pat := range DefaultIgnorePatterns {
		if matchesEither(pat, rawLower, canonical) {
			return canonical, true
		}
	}
	for _, pat := range p.ExtraIgnored {
		if matchesEither(pat, rawLower, canonical) {
			return canonical, true
		}
	}
	return canonical, false
}

// plausibleUsername rejects strings that are structurally not human accounts:
// NT AUTHORITY/NT SERVICE principals, machine accounts, binaries that leaked
// into the user field, and macOS's underscore-prefixed daemon accounts.
func plausibleUsername(name string) bool {
	if len(name) < 2 {
		return false
	}
	if strings.Contains(name, "nt authority") || strings.Contains(name, "nt service") {
		return false
	}
	if strings.HasSuffix(name, "$") {
		return false
	}
	if strings.HasPrefix(name, "_") {
		return false
	}
	for _, suffix := range []string{".exe", ".dll", ".sys", ".com", ".msc", ".scr", ".bat", ".cmd"} {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	return true
}

// matchesEither reports whether pattern matches the raw or the canonical form.
// Matching the raw form lets an admin target one domain's copy of an account
// ("colgate\\zabbix"); matching the canonical form lets a bare name ("zabbix")
// cover every domain and platform.
func matchesEither(pattern, rawLower, canonical string) bool {
	pat := strings.ToLower(strings.TrimSpace(pattern))
	if pat == "" {
		return false
	}
	if MatchGlob(pat, rawLower) || MatchGlob(pat, canonical) {
		return true
	}
	// A bare pattern should also match a domain-qualified raw name even when
	// domain stripping is off.
	if !strings.ContainsAny(pat, `\/`) {
		if i := strings.LastIndexAny(rawLower, `\/`); i >= 0 {
			return MatchGlob(pat, rawLower[i+1:])
		}
	}
	return false
}

// MatchGlob reports whether value matches pattern, where "*" matches any run of
// characters. Both arguments are expected to be lowercased already.
func MatchGlob(pattern, value string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == value
	}
	parts := strings.Split(pattern, "*")
	// Anchor the first segment unless the pattern opens with "*".
	if parts[0] != "" {
		if !strings.HasPrefix(value, parts[0]) {
			return false
		}
		value = value[len(parts[0]):]
	}
	last := parts[len(parts)-1]
	// Anchor the final segment unless the pattern ends with "*".
	if last != "" {
		if !strings.HasSuffix(value, last) {
			return false
		}
		value = value[:len(value)-len(last)]
	}
	// Middle segments must appear in order.
	for _, part := range parts[1 : len(parts)-1] {
		if part == "" {
			continue
		}
		idx := strings.Index(value, part)
		if idx < 0 {
			return false
		}
		value = value[idx+len(part):]
	}
	return true
}
