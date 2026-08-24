package api

import (
	"strings"
	"testing"

	"github.com/rcobb/openlabstats-server/internal/store"
)

// An unknown exe's app/category labels are the agent's own
// software-map.json/PE-metadata resolution. The regression this guards
// against: discarding those labels and seeding a blank "Unknown" placeholder,
// which made every host that reports a recognized-but-uncatalogued exe (e.g.
// POWERPNT.EXE) start that exe's review from scratch instead of landing
// pre-filled.
func TestApplyServerMappingsSeedsHintFromAgentLabels(t *testing.T) {
	body := []byte(`openlabstats_app_usage_seconds_total{app="Microsoft PowerPoint",exe="POWERPNT.EXE",category="Productivity",user="jdoe",hostname="LAB1"} 12`)

	_, unknown := applyServerMappings(body, map[string]*store.SoftwareMapping{})

	got, ok := unknown["POWERPNT.EXE"]
	if !ok {
		t.Fatal("expected POWERPNT.EXE to be reported as unknown")
	}
	if got.DisplayName != "Microsoft PowerPoint" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Microsoft PowerPoint")
	}
	if got.Category != "Productivity" {
		t.Errorf("Category = %q, want %q", got.Category, "Productivity")
	}
}

// An exe the agent itself couldn't resolve (both labels blank/"Unknown") must
// still surface for review, just without a false hint.
func TestApplyServerMappingsHandlesUnresolvedAgentSideExe(t *testing.T) {
	body := []byte(`openlabstats_app_usage_seconds_total{app="Kass.exe",exe="kass.exe",category="Unknown",user="jdoe",hostname="LAB1"} 3`)

	_, unknown := applyServerMappings(body, map[string]*store.SoftwareMapping{})

	got, ok := unknown["kass.exe"]
	if !ok {
		t.Fatal("expected kass.exe to be reported as unknown")
	}
	if got.Category != "Unknown" {
		t.Errorf("Category = %q, want %q", got.Category, "Unknown")
	}
}

// Multiple lines for the same unresolved exe (different users/hosts) must not
// overwrite an already-captured hint with a blank one from a later line.
func TestApplyServerMappingsKeepsFirstHintSeenForRepeatedExe(t *testing.T) {
	body := []byte(strings.Join([]string{
		`m{app="Rhinoceros",exe="Rhinoceros",category="Unknown",user="alice",hostname="LAB1"} 1`,
		`m{app="Rhinoceros",exe="Rhinoceros",category="Unknown",user="bob",hostname="LAB2"} 1`,
	}, "\n"))

	_, unknown := applyServerMappings(body, map[string]*store.SoftwareMapping{})

	if len(unknown) != 1 {
		t.Fatalf("expected exactly one unknown entry, got %d", len(unknown))
	}
	if unknown["Rhinoceros"].DisplayName != "Rhinoceros" {
		t.Errorf("DisplayName = %q, want %q", unknown["Rhinoceros"].DisplayName, "Rhinoceros")
	}
}

// Pre-existing behavior must survive: ignored mappings drop the line, manual
// mappings rewrite app/category, and auto mappings pass through unchanged.
func TestApplyServerMappingsExistingBehaviorUnchanged(t *testing.T) {
	mappings := map[string]*store.SoftwareMapping{
		"ignoreme.exe": {ExeName: "ignoreme.exe", Ignored: true},
		"excel.exe":    {ExeName: "excel.exe", DisplayName: "Microsoft Excel", Category: "Business", Source: "manual"},
		"auto.exe":     {ExeName: "auto.exe", DisplayName: "auto.exe", Category: "Unknown", Source: "auto"},
	}
	body := []byte(strings.Join([]string{
		`m{app="ignoreme.exe",exe="ignoreme.exe",category="Unknown",user="a",hostname="h"} 1`,
		`m{app="EXCEL.EXE",exe="excel.exe",category="Unknown",user="a",hostname="h"} 1`,
		`m{app="auto.exe",exe="auto.exe",category="Unknown",user="a",hostname="h"} 1`,
	}, "\n"))

	out, unknown := applyServerMappings(body, mappings)
	outStr := string(out)

	if strings.Contains(outStr, "ignoreme.exe") {
		t.Error("ignored mapping's line should have been dropped")
	}
	if !strings.Contains(outStr, `app="Microsoft Excel"`) || !strings.Contains(outStr, `category="Business"`) {
		t.Errorf("manual mapping should rewrite app/category, got: %s", outStr)
	}
	if !strings.Contains(outStr, `app="auto.exe"`) {
		t.Errorf("auto mapping should pass through unchanged, got: %s", outStr)
	}
	if len(unknown) != 0 {
		t.Errorf("no exe should be reported unknown when all three are already mapped, got %v", unknown)
	}
}
