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

// Proves a request queued waiting for a semaphore slot still respects
// http.Client.Timeout (which works via context cancellation) instead of
// hanging indefinitely — the bug this fix closed. A naive `t.sem <-
// struct{}{}` blocks forever regardless of the request's context.
func TestConcurrencyLimitedTransportRespectsClientTimeoutWhileQueued(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Hour) // never finishes on its own
	}))
	defer srv.Close()

	sem := make(chan struct{}, 1)
	sem <- struct{}{} // fully occupied: every request must queue

	client := &http.Client{
		Timeout: 200 * time.Millisecond,
		Transport: &concurrencyLimitedTransport{
			sem:       sem,
			transport: http.DefaultTransport,
		},
	}

	start := time.Now()
	_, err := client.Get(srv.URL)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a request stuck queuing past the client timeout")
	}
	if elapsed > 2*time.Second {
		t.Errorf("request stuck queuing took %v to fail, want well under the 200ms timeout's neighborhood (bug: ignored context cancellation)", elapsed)
	}
}
