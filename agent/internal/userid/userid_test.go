package userid

import (
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

func TestResolveIgnoresPseudoAccountsNamedByDomain(t *testing.T) {
	// These have no stable marker in the account half — "Window Manager\\DWM-1"
	// canonicalizes to "dwm-1". The domain component is what identifies them.
	p := NewPolicy()
	for _, raw := range []string{
		`Window Manager\DWM-1`,
		`Window Manager\DWM-2`,
		`Font Driver Host\UMFD-0`,
		`Font Driver Host\UMFD-3`,
		"DWM-1",
	} {
		if _, ignored := p.Resolve(raw); !ignored {
			t.Errorf("Resolve(%q): expected ignored", raw)
		}
	}
	// A real user in a domain that merely resembles one must survive.
	if _, ignored := p.Resolve(`COLGATE\jdoe`); ignored {
		t.Error(`Resolve("COLGATE\jdoe"): expected tracked`)
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

func TestResolveAliasCannotRescueBuiltInIgnored(t *testing.T) {
	// Built-in ignores run first; no alias can bring a service account back.
	p := NewPolicy()
	p.Rules = []Rule{
		{Pattern: "www", Canonical: "web-admin"},
		{Pattern: "installer", Canonical: "installer"},
		{Pattern: "_spotlight", Canonical: "spot"},
	}
	for _, raw := range []string{"www", "installer", "_spotlight"} {
		if _, ignored := p.Resolve(raw); !ignored {
			t.Errorf("Resolve(%q): built-in ignore must not be rescued by an alias", raw)
		}
	}
}

func TestResolveAdminIgnoreBeatsAlias(t *testing.T) {
	for name, rules := range map[string][]Rule{
		"ignore first": {{Pattern: "jdo*", Ignored: true}, {Pattern: "jdoe", Canonical: "jdoe.smith"}},
		"alias first":  {{Pattern: "jdoe", Canonical: "jdoe.smith"}, {Pattern: "jdo*", Ignored: true}},
	} {
		p := NewPolicy()
		p.Rules = rules
		if _, ignored := p.Resolve("jdoe"); !ignored {
			t.Errorf("%s: jdoe should be ignored by the admin ignore rule", name)
		}
	}
	p := NewPolicy()
	p.ExtraIgnored = []string{"jdo*"}
	p.Rules = []Rule{{Pattern: "jdoe", Canonical: "jdoe.smith"}}
	if _, ignored := p.Resolve("jdoe"); !ignored {
		t.Error("ExtraIgnored should beat an alias")
	}
}

func TestResolveAliasSpecificityIndependentOfOrder(t *testing.T) {
	wild := Rule{Pattern: "jdo*", Canonical: "wildtarget"}
	exact := Rule{Pattern: "jdoe", Canonical: "exacttarget"}
	for name, rules := range map[string][]Rule{
		"wildcard first": {wild, exact},
		"exact first":    {exact, wild},
	} {
		p := NewPolicy()
		p.Rules = rules
		if got, ignored := p.Resolve("jdoe"); ignored || got != "exacttarget" {
			t.Errorf("%s: Resolve(jdoe) = (%q, %v), want (exacttarget, false)", name, got, ignored)
		}
		if got, _ := p.Resolve("jdoo"); got != "wildtarget" {
			t.Errorf("%s: Resolve(jdoo) = %q, want wildtarget", name, got)
		}
	}
}

func TestResolveLongerWildcardWins(t *testing.T) {
	for _, rules := range [][]Rule{
		{{Pattern: "j*", Canonical: "short"}, {Pattern: "jdo*", Canonical: "long"}},
		{{Pattern: "jdo*", Canonical: "long"}, {Pattern: "j*", Canonical: "short"}},
	} {
		p := NewPolicy()
		p.Rules = rules
		if got, _ := p.Resolve("jdoe"); got != "long" {
			t.Errorf("Resolve(jdoe) = %q, want long", got)
		}
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

func TestFromServerPrefersExactRulesOverWildcards(t *testing.T) {
	// Both patterns match "svc-jenkins"; the exact one must win regardless of
	// Go's map iteration order.
	for i := 0; i < 20; i++ {
		p := FromServer(true, nil, map[string]string{
			"svc-*":       "service-accounts",
			"svc-jenkins": "jenkins",
		})
		if canonical, _ := p.Resolve("svc-jenkins"); canonical != "jenkins" {
			t.Fatalf(`Resolve("svc-jenkins") = %q, want "jenkins"`, canonical)
		}
	}
}

func TestFromServerIgnorePatterns(t *testing.T) {
	p := FromServer(true, []string{"zabbix"}, nil)
	if _, ignored := p.Resolve(`COLGATE\zabbix`); !ignored {
		t.Error(`Resolve("COLGATE\zabbix"): expected ignored`)
	}
	if _, ignored := p.Resolve("alice"); ignored {
		t.Error(`Resolve("alice"): expected tracked`)
	}
}

func TestHolderAppliesUpdatedPolicy(t *testing.T) {
	h := NewHolder()
	if _, ignored := h.Resolve("zabbix"); ignored {
		t.Error("expected zabbix to be tracked before any rule is pushed")
	}
	h.Set(FromServer(true, []string{"zabbix"}, nil))
	if _, ignored := h.Resolve("zabbix"); !ignored {
		t.Error("expected zabbix to be ignored after the policy update")
	}
}
