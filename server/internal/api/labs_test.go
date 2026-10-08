package api

import (
	"testing"

	"github.com/rcobb/openlabstats-server/internal/store"
)

// Regression: CreateLab/UpdateLab had no duplicate-building+room check at
// all, unlike the case-collision check added to CreateMapping/UpdateMapping
// earlier this session. UpsertAgent's lab lookup (postgres.go: SELECT id
// FROM labs WHERE building = $1 AND room = $2) uses QueryRow, which returns
// an arbitrary one of multiple matching rows if two labs share a
// building+room — findLabCollision must catch that before the write happens.
func TestFindLabCollision(t *testing.T) {
	labs := []store.Lab{
		{ID: "lab-1", Name: "Ryan 113", Building: "Ryan Hall", Room: "113"},
	}

	if got := findLabCollision(labs, "Ryan Hall", "113", ""); got == nil {
		t.Error("expected a collision for a matching building+room, got nil")
	} else if got.ID != "lab-1" {
		t.Errorf("collision returned wrong lab: %+v", got)
	}

	if got := findLabCollision(labs, "Olin Hall", "301", ""); got != nil {
		t.Errorf("a new, unrelated building+room must not be flagged as a collision, got %+v", got)
	}

	if got := findLabCollision(labs, "Ryan Hall", "113", "lab-1"); got != nil {
		t.Errorf("a lab must not collide with its own unchanged building+room during an update, got %+v", got)
	}

	if got := findLabCollision(labs, "", "113", ""); got != nil {
		t.Errorf("a blank building must never be flagged as a collision, got %+v", got)
	}
}
