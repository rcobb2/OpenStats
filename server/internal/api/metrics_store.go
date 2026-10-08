package api

import (
	"sync"
	"time"
)

type agentSnapshot struct {
	body      []byte
	updatedAt time.Time
}

// MetricsStore holds the most-recent Prometheus text-format snapshot pushed
// by each agent. It is the server-side half of the push model: agents POST
// their /metrics output here; Prometheus scrapes GET /metrics/agents which
// returns all fresh snapshots concatenated.
type MetricsStore struct {
	mu        sync.RWMutex
	snapshots map[string]agentSnapshot
	staleness time.Duration
}

// newMetricsStore constructs a MetricsStore and starts its background
// janitor. Set only ever adds or overwrites entries keyed by agent ID; the
// only existing removal path, Delete, is called solely from the manual
// single-agent DeleteAgent handler. The automatic bulk stale-timeout sweep
// (DeleteStaleAgents, driven by runStaleChecker in cmd/server/main.go) is
// pure SQL with no reference to this store, and an agent can also simply go
// quiet forever without ever being deleted at all (StaleTimeoutDays=0 only
// disables the delete, not the offline-marking). In every one of those
// cases, GetAll's own staleness filter stops SERVING the entry, but nothing
// ever removed it from the map — its full metrics-text body stayed in memory
// for the life of the process. Same unbounded-growth shape as
// cachingTransport's own janitor (router.go); fixed the same way here,
// self-contained rather than wiring a cross-package delete hook.
func newMetricsStore() *MetricsStore {
	ms := &MetricsStore{
		snapshots: make(map[string]agentSnapshot),
		staleness: 5 * time.Minute,
	}
	go ms.runJanitor()
	return ms
}

func (ms *MetricsStore) Set(agentID string, body []byte) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.snapshots[agentID] = agentSnapshot{body: body, updatedAt: time.Now()}
}

// Delete removes a single agent's snapshot. Called when an agent is deleted.
func (ms *MetricsStore) Delete(agentID string) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	delete(ms.snapshots, agentID)
}

// GetAll returns the bodies of all non-stale snapshots.
func (ms *MetricsStore) GetAll() [][]byte {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	cutoff := time.Now().Add(-ms.staleness)
	var result [][]byte
	for _, snap := range ms.snapshots {
		if snap.updatedAt.After(cutoff) {
			result = append(result, snap.body)
		}
	}
	return result
}

// prune removes every snapshot that has already aged out of staleness.
// GetAll stops serving such an entry the moment it goes stale, and the
// agent's next heartbeat would just overwrite it via Set anyway, so there is
// no reason to keep it in memory once it can no longer be served. Split out
// from runJanitor so it's testable without waiting on a real ticker.
func (ms *MetricsStore) prune(now time.Time) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	cutoff := now.Add(-ms.staleness)
	for id, snap := range ms.snapshots {
		if snap.updatedAt.Before(cutoff) {
			delete(ms.snapshots, id)
		}
	}
}

// runJanitor periodically prunes stale snapshots. Runs for the lifetime of
// the server process, same as cachingTransport's own janitor and
// runStaleChecker — never explicitly stopped, since the process itself is
// the only thing that ends.
func (ms *MetricsStore) runJanitor() {
	ticker := time.NewTicker(ms.staleness)
	defer ticker.Stop()
	for now := range ticker.C {
		ms.prune(now)
	}
}
