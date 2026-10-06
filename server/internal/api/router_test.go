package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Proves concurrencyLimitedTransport actually bounds in-flight requests —
// the mechanism the production Prometheus-query-contention fix depends on.
func TestConcurrencyLimitedTransportBoundsInFlightRequests(t *testing.T) {
	const limit = 2
	const totalRequests = 6

	var current atomic.Int32
	var maxObserved atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := current.Add(1)
		for {
			old := maxObserved.Load()
			if n <= old || maxObserved.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		current.Add(-1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &concurrencyLimitedTransport{
			sem:       make(chan struct{}, limit),
			transport: http.DefaultTransport,
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(srv.URL)
			if err != nil {
				t.Errorf("request failed: %v", err)
				return
			}
			resp.Body.Close()
		}()
	}
	wg.Wait()

	if got := maxObserved.Load(); got > limit {
		t.Errorf("max concurrent in-flight requests = %d, want <= %d", got, limit)
	}
}
