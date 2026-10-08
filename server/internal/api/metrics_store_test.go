package api

import (
	"testing"
	"time"
)

// Regression: Set only ever added or overwrote entries; the only removal
// path (Delete) is called solely from the manual single-agent delete
// handler, not from the automatic bulk stale-timeout sweep or from an agent
// simply going quiet forever without being deleted. prune (called
// periodically by runJanitor, via newMetricsStore) must remove only entries
// that have actually aged out of staleness — GetAll already stops serving
// those, so keeping them in memory serves no purpose.
func TestMetricsStorePruneRemovesOnlyStaleEntries(t *testing.T) {
	now := time.Now()
	ms := &MetricsStore{
		staleness: 5 * time.Minute,
		snapshots: map[string]agentSnapshot{
			"stale-agent": {body: []byte("stale"), updatedAt: now.Add(-10 * time.Minute)},
			"fresh-agent": {body: []byte("fresh"), updatedAt: now.Add(-1 * time.Minute)},
		},
	}

	ms.prune(now)

	if _, ok := ms.snapshots["stale-agent"]; ok {
		t.Error("stale snapshot should have been pruned")
	}
	if _, ok := ms.snapshots["fresh-agent"]; !ok {
		t.Error("fresh snapshot should not have been pruned")
	}
}

func TestMetricsStoreGetAllExcludesStaleEntries(t *testing.T) {
	now := time.Now()
	ms := &MetricsStore{
		staleness: 5 * time.Minute,
		snapshots: map[string]agentSnapshot{
			"stale-agent": {body: []byte("stale"), updatedAt: now.Add(-10 * time.Minute)},
			"fresh-agent": {body: []byte("fresh"), updatedAt: now.Add(-1 * time.Minute)},
		},
	}

	got := ms.GetAll()
	if len(got) != 1 {
		t.Fatalf("expected 1 fresh snapshot, got %d: %v", len(got), got)
	}
	if string(got[0]) != "fresh" {
		t.Errorf("expected the fresh snapshot's body, got %q", got[0])
	}
}
