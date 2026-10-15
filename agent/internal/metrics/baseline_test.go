package metrics

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
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
