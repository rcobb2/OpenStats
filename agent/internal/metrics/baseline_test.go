package metrics

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testSeries = `openlabstats_app_launches_total{app="A",category="c",exe="a.exe",hostname="h",user="u"}`

var testLabels = []string{"A", "a.exe", "c", "u", "h"}

// scrape serves /metrics through the wrapped handler and returns the value of
// testSeries, or -1 if the series is absent.
func scrape(t *testing.T, h http.Handler) float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(line, testSeries+" ") {
			v, err := strconv.ParseFloat(strings.TrimPrefix(line, testSeries+" "), 64)
			if err != nil {
				t.Fatalf("parse %q: %v", line, err)
			}
			return v
		}
	}
	return -1
}

func expect(t *testing.T, what string, got, want float64) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func TestBaselineNewLabelSetShowsZeroThenValue(t *testing.T) {
	m := NewForTest()
	h := m.ScrapeHandler()
	m.AppLaunches.Inc(testLabels...)
	expect(t, "scrape 1", scrape(t, h), 0)
	expect(t, "scrape 2", scrape(t, h), 1)
}

func TestBaselineAccumulatesBeforeFirstScrape(t *testing.T) {
	m := NewForTest()
	h := m.ScrapeHandler()
	m.AppLaunches.Inc(testLabels...)
	m.AppLaunches.Add(2, testLabels...)
	m.AppLaunches.Inc(testLabels...)
	expect(t, "scrape 1", scrape(t, h), 0)
	expect(t, "scrape 2", scrape(t, h), 4)
}

func TestBaselineExposedIncrementsAreImmediate(t *testing.T) {
	m := NewForTest()
	h := m.ScrapeHandler()
	m.AppLaunches.Inc(testLabels...)
	scrape(t, h)
	scrape(t, h)
	m.AppLaunches.Inc(testLabels...)
	expect(t, "next scrape", scrape(t, h), 2)
}

func TestBaselineRestartStartsAtZero(t *testing.T) {
	old := NewForTest()
	oh := old.ScrapeHandler()
	old.AppLaunches.Inc(testLabels...)
	scrape(t, oh)
	expect(t, "old instance", scrape(t, oh), 1)

	// A restarted agent has a fresh registry; its first event must not appear
	// as an unexplained jump from "series absent" to 1.
	fresh := NewForTest()
	fh := fresh.ScrapeHandler()
	fresh.AppLaunches.Inc(testLabels...)
	expect(t, "post-restart scrape 1", scrape(t, fh), 0)
	expect(t, "post-restart scrape 2", scrape(t, fh), 1)
}

func TestBaselineEventDuringScrapeWaitsForNextScrape(t *testing.T) {
	m := NewForTest()
	var fired bool
	h := m.wrapScrape(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fired {
			fired = true
			// Arrives after the scrape started: this scrape may already have
			// gathered, so it cannot be the one that served the 0.
			m.AppLaunches.Inc(testLabels...)
		}
		m.handler.ServeHTTP(w, r)
	}))
	scrape(t, h) // creates the zero child mid-scrape; must not release it
	expect(t, "scrape 2", scrape(t, h), 0)
	expect(t, "scrape 3", scrape(t, h), 1)
}

// --- push mode ---------------------------------------------------------

// fakeClock drives delivery time explicitly; tests never sleep.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newPushMetrics() (*Metrics, *fakeClock) {
	m := NewForTest()
	clk := &fakeClock{t: time.Unix(1_000_000, 0)}
	m.now = clk.now
	m.EnablePushMode(0) // default 75s hold
	return m, clk
}

// push simulates one PushMetrics cycle: begin, gather the local body, then
// report delivery only if ok. It returns the value the pushed body carried.
func push(t *testing.T, m *Metrics, ok bool) float64 {
	t.Helper()
	token := m.BeginDelivery()
	v := scrape(t, m.ScrapeHandler())
	if ok {
		m.Delivered(token)
	}
	return v
}

func TestPushFailedFirstPushDoesNotStartClock(t *testing.T) {
	m, clk := newPushMetrics()
	m.AppLaunches.Inc(testLabels...)
	expect(t, "failed push body", push(t, m, false), 0)
	clk.advance(30 * time.Second)
	push(t, m, false)
	clk.advance(60 * time.Second)                               // would be past the hold had the clock started
	expect(t, "still held after failures", push(t, m, true), 0) // first success starts the clock
	clk.advance(30 * time.Second)
	expect(t, "30s after first success", push(t, m, true), 0)
	clk.advance(45 * time.Second)
	push(t, m, true) // 75s after first success: released here
	expect(t, "next body", push(t, m, true), 1)
}

func TestPushReleasedOnFirstDeliveryAtOrAfterHold(t *testing.T) {
	m, clk := newPushMetrics()
	m.AppLaunches.Inc(testLabels...)
	expect(t, "t=0", push(t, m, true), 0)
	for _, at := range []int{30, 60} {
		clk.advance(30 * time.Second)
		expect(t, "t="+strconv.Itoa(at), push(t, m, true), 0)
	}
	clk.advance(30 * time.Second) // t=90 >= 75: this delivery releases
	expect(t, "t=90 body (gathered before release)", push(t, m, true), 0)
	clk.advance(30 * time.Second)
	expect(t, "t=120 body", push(t, m, true), 1)
}

// The Copilot scenario: the zero delivered at t=0 can be replaced by the t=30
// push before Prometheus scrapes the server, so the increment must not be in
// the t=30 body.
func TestPushZeroNotOverwrittenByIncrementAt30s(t *testing.T) {
	m, clk := newPushMetrics()
	m.AppLaunches.Inc(testLabels...)
	push(t, m, true)
	clk.advance(30 * time.Second)
	expect(t, "t=30 body", push(t, m, true), 0)
	expect(t, "t=30 next local read", scrape(t, m.ScrapeHandler()), 0)
}

func TestPushLabelSetCreatedAfterBeginNotDelivered(t *testing.T) {
	m, clk := newPushMetrics()
	token := m.BeginDelivery()
	m.AppLaunches.Inc(testLabels...) // after begin: body may not contain the zero
	m.Delivered(token)
	clk.advance(200 * time.Second)
	token = m.BeginDelivery()
	m.Delivered(token) // first delivery that can contain the zero: starts clock only
	clk.advance(200 * time.Second)
	expect(t, "body", scrape(t, m.ScrapeHandler()), 0)
	m.Delivered(m.BeginDelivery()) // now past hold -> release
	expect(t, "after release", scrape(t, m.ScrapeHandler()), 1)
}

func TestPushLocalReadsNeverRelease(t *testing.T) {
	m, clk := newPushMetrics()
	h := m.ScrapeHandler()
	m.AppLaunches.Inc(testLabels...)
	for i := 0; i < 5; i++ {
		clk.advance(time.Hour)
		expect(t, "local read", scrape(t, h), 0)
	}
	m.ReleasePending() // test hook still force-releases in push mode
	expect(t, "after ReleasePending", scrape(t, h), 1)
}

func TestPushExposedIncrementsAreImmediate(t *testing.T) {
	m, clk := newPushMetrics()
	m.AppLaunches.Inc(testLabels...)
	push(t, m, true)
	clk.advance(30 * time.Second)
	push(t, m, true)
	clk.advance(60 * time.Second)
	push(t, m, true) // released
	m.AppLaunches.Inc(testLabels...)
	expect(t, "immediate", scrape(t, m.ScrapeHandler()), 2)
}
