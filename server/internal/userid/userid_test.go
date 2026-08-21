package userid

import (
	"regexp"
	"strings"
	"testing"
)

func TestCanonicalizeMergesPlatforms(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`COLGATE\jdoe`, "jdoe"},
		{`colgate\JDoe`, "jdoe"},
		{"jdoe", "jdoe"},
		{"JDOE", "jdoe"},
		{"jdoe@colgate.edu", "jdoe"},
		{`COLGATE/jdoe`, "jdoe"},
		{"  jdoe  ", "jdoe"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Canonicalize(c.raw, true); got != c.want {
			t.Errorf("Canonicalize(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestCanonicalizeWithoutDomainStripping(t *testing.T) {
	if got := Canonicalize(`COLGATE\jdoe`, false); got != `colgate\jdoe` {
		t.Errorf(`Canonicalize("COLGATE\jdoe", false) = %q, want "colgate\jdoe"`, got)
	}
}

func TestResolveIgnoresSystemAccountsByDefault(t *testing.T) {
	p := NewPolicy()
	ignored := []string{
		"", "root", "daemon", "nobody", "wheel", "_spotlight", "_www",
		"SYSTEM", "LOCAL SERVICE", "NETWORK SERVICE", `NT AUTHORITY\SYSTEM`,
		`NT SERVICE\WdiServiceHost`, "MACHINE$", "notepad.exe", "x",
		`COLGATE\SYSTEM`, "UMFD-3", "panopto_upload",
	}
	for _, raw := range ignored {
		if _, isIgnored := p.Resolve(raw); !isIgnored {
			t.Errorf("Resolve(%q): expected ignored", raw)
		}
	}

	tracked := []string{"alice", "bob.smith", "student01", `COLGATE\jdoe`, "rcobb@colgate.edu"}
	for _, raw := range tracked {
		if _, isIgnored := p.Resolve(raw); isIgnored {
			t.Errorf("Resolve(%q): expected tracked", raw)
		}
	}
}

func TestResolveIgnoreRules(t *testing.T) {
	p := NewPolicy()
	p.Rules = []Rule{
		{Pattern: "zabbix", Ignored: true},
		{Pattern: "svc-*", Ignored: true},
	}

	// A bare ignore pattern covers the account on both platforms.
	for _, raw := range []string{"zabbix", `COLGATE\zabbix`, "ZABBIX", "zabbix@colgate.edu", "svc-backup", `COLGATE\svc-jenkins`} {
		if _, ignored := p.Resolve(raw); !ignored {
			t.Errorf("Resolve(%q): expected ignored", raw)
		}
	}
	// A similarly named human account is unaffected.
	if _, ignored := p.Resolve("zabbixadmin"); ignored {
		t.Error(`Resolve("zabbixadmin"): expected tracked`)
	}
}

func TestResolveAliasMergesIdentities(t *testing.T) {
	p := NewPolicy()
	p.Rules = []Rule{{Pattern: "jdoe2", Canonical: "jdoe"}}

	for _, raw := range []string{"jdoe2", `COLGATE\jdoe2`, "JDoe2"} {
		canonical, ignored := p.Resolve(raw)
		if ignored {
			t.Fatalf("Resolve(%q): unexpectedly ignored", raw)
		}
		if canonical != "jdoe" {
			t.Errorf("Resolve(%q) = %q, want \"jdoe\"", raw, canonical)
		}
	}
}

func TestResolveRuleRescuesDefaultIgnoredAccount(t *testing.T) {
	// An explicit rule outranks the built-in ignore list, so a lab whose real
	// account name collides with a service-account name can still be tracked.
	p := NewPolicy()
	p.Rules = []Rule{{Pattern: "installer", Canonical: "installer"}}
	if canonical, ignored := p.Resolve("installer"); ignored || canonical != "installer" {
		t.Errorf(`Resolve("installer") = (%q, %v), want ("installer", false)`, canonical, ignored)
	}
}

func TestResolveDomainScopedIgnoreRule(t *testing.T) {
	// A domain-qualified pattern must not ignore the same name in other domains.
	p := NewPolicy()
	p.Rules = []Rule{{Pattern: `colgate\admin`, Ignored: true}}
	if _, ignored := p.Resolve(`COLGATE\admin`); !ignored {
		t.Error(`Resolve("COLGATE\admin"): expected ignored`)
	}
	if _, ignored := p.Resolve(`OTHER\admin`); ignored {
		t.Error(`Resolve("OTHER\admin"): expected tracked`)
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, value string
		want           bool
	}{
		{"zabbix", "zabbix", true},
		{"zabbix", "zabbixadmin", false},
		{"svc-*", "svc-backup", true},
		{"svc-*", "svcbackup", false},
		{"*-svc", "backup-svc", true},
		{"*admin*", "the-admin-user", true},
		{"*", "anything", true},
		{"umfd-*", "umfd-3", true},
	}
	for _, c := range cases {
		if got := MatchGlob(c.pattern, c.value); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", c.pattern, c.value, got, c.want)
		}
	}
}

// TestIgnoreMatcherAgreesWithResolve guards the property the whole feature rests
// on: what the agent drops at collection time and what PromQL filters out at
// report time must be the same set of accounts.
func TestIgnoreMatcherAgreesWithResolve(t *testing.T) {
	p := NewPolicy()
	p.Rules = []Rule{
		{Pattern: "zabbix", Ignored: true},
		{Pattern: "svc-*", Ignored: true},
		{Pattern: "jdoe2", Canonical: "jdoe"},
	}

	matcher := p.IgnoreMatcher()
	const prefix = "user!~`"
	if !strings.HasPrefix(matcher, prefix) || !strings.HasSuffix(matcher, "`") {
		t.Fatalf("IgnoreMatcher() = %q, want a backquoted user!~ matcher", matcher)
	}
	// Prometheus anchors label regexes at both ends.
	pattern := "^(?:" + strings.TrimSuffix(strings.TrimPrefix(matcher, prefix), "`") + ")$"
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("IgnoreMatcher() produced an invalid regex %q: %v", pattern, err)
	}

	samples := []string{
		"zabbix", `COLGATE\zabbix`, "ZABBIX", "zabbix@colgate.edu",
		"svc-backup", `COLGATE\svc-jenkins`,
		"jdoe", `COLGATE\jdoe`, "jdoe2", "alice", "bob.smith", "zabbixadmin",
		"root", "_spotlight", "SYSTEM", `NT AUTHORITY\SYSTEM`, "MACHINE$",
		"UMFD-3", "panopto_upload", "Font Driver Host",
	}
	for _, raw := range samples {
		_, ignored := p.Resolve(raw)
		if excluded := re.MatchString(raw); excluded != ignored {
			t.Errorf("%q: Resolve ignored=%v but PromQL matcher excluded=%v", raw, ignored, excluded)
		}
	}
}

func TestIgnoreMatcherRejectsUnsafePatterns(t *testing.T) {
	p := NewPolicy()
	p.Rules = []Rule{{Pattern: "bad`\"} or vector(1) #", Ignored: true}}
	matcher := p.IgnoreMatcher()
	if strings.Contains(matcher, "`\"") || strings.Count(matcher, "`") != 2 {
		t.Errorf("IgnoreMatcher() leaked unsafe pattern characters: %q", matcher)
	}
}
