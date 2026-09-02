package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// Exercises the DB-backed slot-claim SQL (advisory lock, interval math, renew vs
// claim, grace expiry, release) against a real Postgres. Skipped unless
// OPENSTATS_TEST_DSN is set, e.g.:
//
//	OPENSTATS_TEST_DSN=postgres://postgres:test@localhost:55433/openstats?sslmode=disable \
//	  go test ./internal/store/ -run TestClaimUpdateSlot -v
func TestClaimUpdateSlotIntegration(t *testing.T) {
	dsn := os.Getenv("OPENSTATS_TEST_DSN")
	if dsn == "" {
		t.Skip("set OPENSTATS_TEST_DSN to run the rollout store integration test")
	}
	ctx := context.Background()
	st, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	// Fresh agents table for a deterministic run.
	if _, err := st.pool.Exec(ctx, `DELETE FROM agents`); err != nil {
		t.Fatalf("clean agents: %v", err)
	}
	seed := func(id, ver string) {
		if err := st.UpsertAgent(ctx, &Agent{ID: id, Hostname: id, OSVersion: "macOS 14", AgentVersion: ver}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("a1", "0.1.10")
	seed("a2", "0.1.10")
	seed("a3", "0.1.10")

	grace := 15 * time.Minute
	const target = "0.4.0"

	// Budget of 2: first two claims succeed, third is denied.
	if ok, err := st.ClaimUpdateSlot(ctx, "a1", target, 2, grace); err != nil || !ok {
		t.Fatalf("a1 claim: ok=%v err=%v (want true)", ok, err)
	}
	if ok, err := st.ClaimUpdateSlot(ctx, "a2", target, 2, grace); err != nil || !ok {
		t.Fatalf("a2 claim: ok=%v err=%v (want true)", ok, err)
	}
	if ok, err := st.ClaimUpdateSlot(ctx, "a3", target, 2, grace); err != nil || ok {
		t.Fatalf("a3 claim: ok=%v err=%v (want false — budget full)", ok, err)
	}

	// a1 re-heartbeats within grace: renew → not re-offered, but still holds slot.
	if ok, err := st.ClaimUpdateSlot(ctx, "a1", target, 2, grace); err != nil || ok {
		t.Fatalf("a1 renew: ok=%v err=%v (want false — already in flight)", ok, err)
	}
	if n, err := st.CountInFlightUpdates(ctx, grace); err != nil || n != 2 {
		t.Fatalf("in-flight after renew = %d err=%v (want 2)", n, err)
	}

	// a1 completes → release frees a slot; a3 can now claim.
	if err := st.ReleaseUpdateSlot(ctx, "a1"); err != nil {
		t.Fatalf("release a1: %v", err)
	}
	if n, err := st.CountInFlightUpdates(ctx, grace); err != nil || n != 1 {
		t.Fatalf("in-flight after release = %d err=%v (want 1)", n, err)
	}
	if ok, err := st.ClaimUpdateSlot(ctx, "a3", target, 2, grace); err != nil || !ok {
		t.Fatalf("a3 claim after release: ok=%v err=%v (want true)", ok, err)
	}

	// Simulate a2's offer aging past the grace window → its slot should free,
	// so with budget 2 and only a3 fresh, a new agent can claim.
	if _, err := st.pool.Exec(ctx,
		`UPDATE agents SET update_offered_at = NOW() - INTERVAL '20 minutes' WHERE id = 'a2'`); err != nil {
		t.Fatalf("age a2: %v", err)
	}
	if n, err := st.CountInFlightUpdates(ctx, grace); err != nil || n != 1 {
		t.Fatalf("in-flight after aging a2 = %d err=%v (want 1 — only a3)", n, err)
	}
	seed("a4", "0.1.10")
	if ok, err := st.ClaimUpdateSlot(ctx, "a4", target, 2, grace); err != nil || !ok {
		t.Fatalf("a4 claim (a2 aged out): ok=%v err=%v (want true)", ok, err)
	}
}
