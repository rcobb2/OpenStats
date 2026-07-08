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

func newMetricsStore() *MetricsStore {
	return &MetricsStore{
		snapshots: make(map[string]agentSnapshot),
		staleness: 5 * time.Minute,
	}
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
