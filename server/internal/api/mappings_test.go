package api

import (
	"testing"
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
