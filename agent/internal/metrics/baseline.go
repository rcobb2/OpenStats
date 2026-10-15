package metrics

import (
	"net/http"
	"strings"
	"sync"
	"time"

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
// until something downstream has demonstrably seen that 0; then the full value
// is exposed and the delta is visible. Who counts as "downstream" depends on
// how Prometheus reaches this agent:
//
//   - Local-scrape mode (no server URL, Prometheus scrapes :9183 directly):
//     release after a /metrics response is served (Metrics.ScrapeHandler).
//   - Push mode (production): Prometheus never talks to the agent. The agent
//     POSTs its snapshot to the server, which keeps only the latest one per
//     agent, and Prometheus scrapes that store. A local /metrics read proves
//     nothing (the POST may fail), and a delivered zero can be overwritten by
//     the next push before Prometheus scrapes. So release is driven by
//     successful deliveries and a minimum hold; see delivered.
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

	// Push mode only. firstDelivered is when a successful push whose body
	// contained the zero first landed on the server (zero value = not yet).
	firstDelivered time.Time
}

// DefaultPushHold is how long a delivered zero must stay the server's latest
// snapshot before the held amount is released. The agent pushes every 30s and
// Prometheus scrapes the server's snapshot store every 30s, so push interval ==
// scrape interval: the zero delivered at t0 can be replaced by the next push at
// t0+30s, possibly before the scrape. 75s (2.5 scrape intervals) guarantees at
// least one full scrape interval saw the zero even if one push in between
// fails (a failed push leaves the previous zero snapshot in place, which is
// still zero).
const DefaultPushHold = 75 * time.Second

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

// beginDelivery is the push-mode analogue of beginScrape: call it before the
// push gathers its body. Label sets created after it may be missing from that
// body, so a delivery of this token must not count for them.
func (b *BaselineCounterVec) beginDelivery() uint64 { return b.beginScrape() }

// delivered records that the push identified by snap was accepted by the server
// at time now. A pending label set records the first such delivery whose body
// contained its zero; it is released only on a later delivery once the zero has
// been the server's snapshot for at least hold. Evaluating only on deliveries
// (not on a timer) means a server outage can never release anything.
func (b *BaselineCounterVec) delivered(snap uint64, now time.Time, hold time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, p := range b.pending {
		if p.created > snap {
			continue // zero was not in that body
		}
		if p.firstDelivered.IsZero() {
			p.firstDelivered = now
			continue
		}
		if now.Sub(p.firstDelivered) < hold {
			continue
		}
		b.vec.WithLabelValues(p.labels...).Add(p.amount)
		b.exposed[key] = struct{}{}
		delete(b.pending, key)
	}
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

// EnablePushMode switches to delivery-driven release (see BaselineCounterVec).
// Call it when the agent pushes to a server that Prometheus scrapes instead of
// scraping the agent. hold <= 0 selects DefaultPushHold. After this, local
// /metrics reads no longer release anything.
func (m *Metrics) EnablePushMode(hold time.Duration) {
	if hold <= 0 {
		hold = DefaultPushHold
	}
	m.deliveryMu.Lock()
	m.hold = hold
	m.deliveryMu.Unlock()
	m.pushMode.Store(true)
}

// BeginDelivery must be called before a push gathers its body (before the
// local /metrics GET). The returned token identifies which label sets that body
// can contain; pass it to Delivered only if the server accepted the push.
func (m *Metrics) BeginDelivery() uint64 {
	snaps := make([]uint64, len(m.baselines))
	for i, b := range m.baselines {
		snaps[i] = b.beginDelivery()
	}
	m.deliveryMu.Lock()
	defer m.deliveryMu.Unlock()
	m.deliveryID++
	// Pushes are sequential; an undelivered earlier token is simply dropped,
	// which only ever delays a release.
	m.deliverySnaps = map[uint64][]uint64{m.deliveryID: snaps}
	return m.deliveryID
}

// Delivered reports that the push identified by token was accepted (2xx) by the
// server. A failed push must not call it.
func (m *Metrics) Delivered(token uint64) {
	m.deliveryMu.Lock()
	snaps, ok := m.deliverySnaps[token]
	delete(m.deliverySnaps, token)
	hold := m.hold
	now := m.clock()
	m.deliveryMu.Unlock()
	if !ok || !m.pushMode.Load() {
		return
	}
	for i, b := range m.baselines {
		b.delivered(snaps[i], now, hold)
	}
}

func (m *Metrics) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// ScrapeHandler returns the /metrics handler for this Metrics instance. In
// local-scrape mode, after each scrape completes it releases the amounts held
// behind zero baselines that existed before that scrape began. In push mode it
// serves normally and releases nothing.
func (m *Metrics) ScrapeHandler() http.Handler {
	return m.wrapScrape(m.handler)
}

func (m *Metrics) wrapScrape(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.pushMode.Load() {
			// The push's own local GET lands here, and it proves nothing about
			// what the server received.
			h.ServeHTTP(w, r)
			return
		}
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
