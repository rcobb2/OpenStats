package userid

import (
	"strings"
)

// structuralIgnoreRegexes mirror plausibleUsername for PromQL: machine accounts,
// macOS daemon accounts, and NT AUTHORITY/NT SERVICE principals.
var structuralIgnoreRegexes = []string{
	`.`,                // single-character names are never real accounts
	`.*\$`,             // machine accounts (DOMAIN\PC123$)
	`(?:[^\\]*\\)?_.*`, // macOS daemon accounts (_spotlight)
	`nt authority\\.*`,
	`nt service\\.*`,
	`.*\.(?:exe|dll|sys|com|msc|scr|bat|cmd)`,
}

// IgnoreMatcher returns a PromQL label matcher that excludes every ignored
// account, e.g. user!~`(?i)(?:...)`. It is applied to the raw `user` label as
// stored in Prometheus, so each pattern also matches a "DOMAIN\" -prefixed or
// "@domain" -suffixed form of itself.
//
// The regex is emitted inside a backquoted PromQL string so that backslashes
// (unavoidable in DOMAIN\user) need no escaping. Patterns containing characters
// that are not valid in a username are skipped rather than escaped, which keeps
// admin-supplied text from reaching PromQL as syntax.
func (p *Policy) IgnoreMatcher() string {
	alts := make([]string, 0, len(DefaultIgnorePatterns)+len(p.ExtraIgnored)+len(p.Rules)+len(structuralIgnoreRegexes))
	alts = append(alts, structuralIgnoreRegexes...)

	seen := make(map[string]bool)
	add := func(pattern string) {
		re, ok := globToRegex(pattern)
		if !ok || seen[re] {
			return
		}
		seen[re] = true
		alts = append(alts, re)
	}

	for _, pat := range DefaultIgnorePatterns {
		add(pat)
	}
	for _, pat := range p.ExtraIgnored {
		add(pat)
	}
	for _, rule := range p.Rules {
		if rule.Ignored {
			add(rule.Pattern)
		}
	}

	return "user!~`(?i)(?:" + strings.Join(alts, "|") + ")`"
}

// globToRegex converts one ignore pattern into an RE2 alternative matching the
// full raw label value. Returns ok=false for patterns that cannot appear in a
// username, which are dropped instead of being escaped into the query.
func globToRegex(pattern string) (string, bool) {
	pat := strings.ToLower(strings.TrimSpace(pattern))
	if pat == "" {
		return "", false
	}

	var b strings.Builder
	for _, c := range pat {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == ' ' || c == '-' || c == '_':
			b.WriteRune(c)
		case c == '.' || c == '$' || c == '+' || c == '@':
			b.WriteString(`\`)
			b.WriteRune(c)
		case c == '\\' || c == '/':
			b.WriteString(`\\`)
		case c == '*':
			b.WriteString(`.*`)
		default:
			// Anything else (quotes, backticks, parens, braces) is not a valid
			// username character — reject the whole pattern.
			return "", false
		}
	}
	body := b.String()
	if body == "" {
		return "", false
	}

	// A domain-qualified pattern is used verbatim; a bare one also matches the
	// same account seen with a domain prefix or a UPN suffix.
	if strings.Contains(pat, `\`) || strings.Contains(pat, `/`) {
		return body, true
	}
	return `(?:[^\\]*\\)?` + body + `(?:@[^\\]*)?`, true
}
