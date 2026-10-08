package api

import (
	"testing"

	"github.com/rcobb/openlabstats-server/internal/store"
)

func TestValidCategories(t *testing.T) {
	tests := []struct {
		category string
		want     bool
	}{
		{"Business", true},
		{"Scientific", true},
		{"Unknown", true},
		{"", true}, // uncategorized is allowed
		// The exact typos that split real apps' usage across two buckets.
		{"Productive", false},
		{"business", false}, // wrong case
		{"Not A Real Category", false},
	}
	for _, tt := range tests {
		if got := validCategories[tt.category]; got != tt.want {
			t.Errorf("validCategories[%q] = %v, want %v", tt.category, got, tt.want)
		}
	}
}

// Regression: UpdateMapping used to skip the case-collision check
// CreateMapping already had, even though both upsert by exeName via the same
// UpsertMapping call — and openstatsctl's `mappings set <exe-name>` (an
// operator typing an arbitrary exe name) reaches UpdateMapping as directly as
// it does CreateMapping. Without the check, "excel.exe" alongside an
// auto-discovered "EXCEL.EXE" would silently collide at match time instead of
// being rejected with a clear 409.
func TestFindCaseCollision(t *testing.T) {
	existing := map[string]*store.SoftwareMapping{
		"excel.exe": {ExeName: "EXCEL.EXE", DisplayName: "Microsoft Excel"},
	}

	if got := findCaseCollision(existing, "excel.exe"); got == nil {
		t.Error("expected a collision for a different-cased exe name, got nil")
	} else if got.ExeName != "EXCEL.EXE" {
		t.Errorf("collision returned wrong mapping: %+v", got)
	}

	if got := findCaseCollision(existing, "EXCEL.EXE"); got != nil {
		t.Errorf("exact-case match (normal update-in-place) must not be flagged as a collision, got %+v", got)
	}

	if got := findCaseCollision(existing, "WORD.EXE"); got != nil {
		t.Errorf("a new, unrelated exe name must not be flagged as a collision, got %+v", got)
	}
}

func TestValidCategoryNamesExcludesBlankAndIsSorted(t *testing.T) {
	names := validCategoryNames()
	for _, n := range names {
		if n == "" {
			t.Error("validCategoryNames() should not include the blank/uncategorized entry")
		}
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("validCategoryNames() not sorted: %q before %q", names[i-1], names[i])
		}
	}
}
