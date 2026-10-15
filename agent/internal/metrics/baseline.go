package metrics

import (
	"net/http"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// BaselineCounterVec wraps a CounterVec so that every label set is first
// exposed at 0 and only then incremented.
//
// Prometheus' increase()/rate() cannot count the first sample of a series that
// first appears already non-zero: there is no earlier point to subtract. After
// an agent restart (counters are deliberately not restored) every label set
// reappears via its first Add at a non-zero value, so that first event would be
// lost. Instead a new label set is created at 0 and the amount is held back
// until a real scrape has served that 0 (see Metrics.ScrapeHandler); the next
// scrape then shows the full value and the delta is visible. No timer is
// involved: release is tied to actual scrapes, so a slow or missed scrape
// cannot let the zero and the first value collapse into one sample.
type BaselineCounterVec struct {
	vec *prometheus.CounterVec

	mu      sync.Mutex
	epoch   uint64              // bumped at the start of every scrape
	exposed map[string]struct{} // label sets already served at a zero baseline
	pending map[string]*pendingAdd
}

type pendingAdd struct {
	labels  []string
	amount  float64
	created uint64 // epoch when the zero child was created
}

// NewBaselineCounterVec wraps cv.
func NewBaselineCounterVec(cv *prometheus.CounterVec) *BaselineCounterVec {
	return &BaselineCounterVec{
		vec:     cv,
		exposed: make(map[string]struct{}),
		pending: make(map[string]*pendingAdd),
	}
}

// Vec returns the underlying CounterVec (registration, tests).
func (b *BaselineCounterVec) Vec() *prometheus.CounterVec { return b.vec }

// Inc adds 1 to the counter for labels.
func (b *BaselineCounterVec) Inc(labels ...string) { b.Add(1, labels...) }

// Add adds v to the counter for labels, deferring it behind a scraped zero
// baseline if the label set is new.
func (b *BaselineCounterVec) Add(v float64, labels ...string) {
	key := strings.Join(labels, "\xff")

	b.mu.Lock()
	if _, ok := b.exposed[key]; ok {
		b.mu.Unlock()
		b.vec.WithLabelValues(labels...).Add(v)
		return
	}
	if p, ok := b.pending[key]; ok {
		p.amount += v
		b.mu.Unlock()
		return
	}
	// Create the child at 0 so the next scrape serves the baseline.
	b.vec.WithLabelValues(labels...)
	b.pending[key] = &pendingAdd{
		labels:  append([]string(nil), labels...),
		amount:  v,
		created: b.epoch,
	}
	b.mu.Unlock()
}

// beginScrape marks the start of a scrape and returns the snapshot to hand to
// release. Anything created after this call carries a later epoch and waits
// for the next scrape, because this scrape may have gathered before it existed.
func (b *BaselineCounterVec) beginScrape() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	snap := b.epoch
	b.epoch++
	return snap
}

// release applies pending amounts whose zero child existed before the scrape
// identified by snap started.
func (b *BaselineCounterVec) release(snap uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, p := range b.pending {
		if p.created > snap {
			continue
		}
		b.vec.WithLabelValues(p.labels...).Add(p.amount)
		b.exposed[key] = struct{}{}
		delete(b.pending, key)
	}
}

// ReleasePending applies every pending amount immediately, as if a scrape had
// served all baselines. Test hook; production code releases via ScrapeHandler.
func (b *BaselineCounterVec) ReleasePending() {
	b.mu.Lock()
	snap := b.epoch
	b.mu.Unlock()
	b.release(snap)
}

// ReleasePending releases pending baselines on every counter. Test hook.
func (m *Metrics) ReleasePending() {
	for _, b := range m.baselines {
		b.ReleasePending()
	}
}

// ScrapeHandler returns the /metrics handler for this Metrics instance. After
// each scrape completes it releases the amounts held behind zero baselines
// that existed before that scrape began.
func (m *Metrics) ScrapeHandler() http.Handler {
	return m.wrapScrape(m.handler)
}

func (m *Metrics) wrapScrape(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snaps := make([]uint64, len(m.baselines))
		for i, b := range m.baselines {
			snaps[i] = b.beginScrape()
		}
		h.ServeHTTP(w, r)
		for i, b := range m.baselines {
			b.release(snaps[i])
		}
	})
}
