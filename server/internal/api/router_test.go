package api

import (
	"io"
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

func newTestCachingTransport() *cachingTransport {
	return &cachingTransport{
		ttl:       time.Hour, // tests control freshness explicitly via expires/sleep
		cache:     make(map[string]cachedEntry),
		transport: http.DefaultTransport,
	}
}

// Concurrent identical GET requests must collapse into one upstream call —
// the mechanism that prevents several report panels (or viewers) querying
// the same expensive range from all independently hitting Prometheus.
func TestCachingTransportCoalescesConcurrentIdenticalRequests(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()

	client := &http.Client{Transport: newTestCachingTransport()}

	const concurrent = 10
	var wg sync.WaitGroup
	bodies := make([]string, concurrent)
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := client.Get(srv.URL + "?query=same")
			if err != nil {
				t.Errorf("request %d failed: %v", idx, err)
				return
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			bodies[idx] = string(b)
		}(i)
	}
	wg.Wait()

	if got := hits.Load(); got != 1 {
		t.Errorf("upstream hits = %d, want exactly 1 (requests should coalesce)", got)
	}
	for i, b := range bodies {
		if b != `{"status":"success"}` {
			t.Errorf("caller %d got body %q, want the real response (each caller needs its own readable body)", i, b)
		}
	}
}

// A cached response must be served without a second upstream call within TTL.
func TestCachingTransportServesFromCacheWithinTTL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := &http.Client{Transport: newTestCachingTransport()}

	for i := 0; i < 5; i++ {
		resp, err := client.Get(srv.URL + "?query=x")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
	}

	if got := hits.Load(); got != 1 {
		t.Errorf("upstream hits = %d, want 1 (later requests should be served from cache)", got)
	}
}

// Once a cache entry expires, the next request must go upstream again.
func TestCachingTransportRefetchesAfterExpiry(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	ct := newTestCachingTransport()
	ct.ttl = 30 * time.Millisecond
	client := &http.Client{Transport: ct}

	resp, err := client.Get(srv.URL + "?query=x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	time.Sleep(60 * time.Millisecond)

	resp, err = client.Get(srv.URL + "?query=x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if got := hits.Load(); got != 2 {
		t.Errorf("upstream hits = %d, want 2 (cache entry should have expired)", got)
	}
}

// A failed (non-200) upstream response must not be cached — a transient
// Prometheus error shouldn't be frozen in place for everyone else for the
// rest of the TTL window.
func TestCachingTransportDoesNotCacheFailures(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"failed to reach Prometheus"}`))
	}))
	defer srv.Close()

	client := &http.Client{Transport: newTestCachingTransport()}

	for i := 0; i < 3; i++ {
		resp, err := client.Get(srv.URL + "?query=x")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
	}

	if got := hits.Load(); got != 3 {
		t.Errorf("upstream hits = %d, want 3 (failures must not be cached)", got)
	}
}

// Non-GET requests must bypass the cache entirely (defensive: every current
// Prometheus call is a GET, but this transport shouldn't silently cache a
// write if one is ever added).
func TestCachingTransportBypassesNonGET(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Transport: newTestCachingTransport()}

	for i := 0; i < 3; i++ {
		resp, err := client.Post(srv.URL, "application/json", nil)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
	}

	if got := hits.Load(); got != 3 {
		t.Errorf("upstream hits = %d, want 3 (POST requests must not be cached)", got)
	}
}

// Regression: RoundTrip only ever added cache entries, never removed them —
// every distinct query URL (including one embedding a unique &time=<ts> or
// &start=<ts>&end=<ts> from a custom-range report) is its own key, so a
// long-running server process accumulated one permanent entry per distinct
// range ever requested. prune (called periodically by runJanitor, via
// newCachingTransport) must remove only entries that have actually expired.
func TestCachingTransportPruneRemovesOnlyExpiredEntries(t *testing.T) {
	now := time.Now()
	ct := &cachingTransport{
		cache: map[string]cachedEntry{
			"expired": {expires: now.Add(-time.Minute)},
			"fresh":   {expires: now.Add(time.Minute)},
		},
	}

	ct.prune(now)

	if _, ok := ct.cache["expired"]; ok {
		t.Error("expired entry should have been pruned")
	}
	if _, ok := ct.cache["fresh"]; !ok {
		t.Error("fresh entry should not have been pruned")
	}
}
