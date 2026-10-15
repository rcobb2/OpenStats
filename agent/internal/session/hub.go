// Package session carries foreground-window and idle-time samples from
// per-user-session helper processes to the agent service.
//
// The service cannot sample these itself: on Windows it runs in session 0,
// which has no access to a user's desktop, and on macOS a root LaunchDaemon sits
// outside the user's GUI session. So the service launches a copy of its own
// binary inside each user's session (the "session helper"); the helper samples
// locally and reports over its stdout pipe, and the Hub collects the results.
package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"sync"
	"time"
)

// Sample is one observation from a helper: the foreground process and how
// long the session has gone without keyboard/mouse input.
type Sample struct {
	PID  uint32        // 0 means no foreground window
	Idle time.Duration // 0 when idle time could not be determined
}

// wireSample is the helper's JSON line format.
type wireSample struct {
	PID    uint32 `json:"pid"`
	IdleMs int64  `json:"idleMs"`
}

// MaxLineBytes bounds one helper line. A legitimate line is ~40 bytes; the
// helper runs as an unprivileged user, so its output is untrusted and must not
// be able to make the service buffer without limit.
const MaxLineBytes = 4 * 1024

// FreshFor is how long a sample stays credible. Helpers report every 5s, so
// 15s tolerates two missed reports before a helper stops counting as connected.
const FreshFor = 15 * time.Second

// ParseLine decodes one helper line. It reports false for malformed input,
// including a negative idle time.
func ParseLine(line []byte) (Sample, bool) {
	var w wireSample
	if err := json.Unmarshal(line, &w); err != nil || w.IdleMs < 0 {
		return Sample{}, false
	}
	return Sample{PID: w.PID, Idle: time.Duration(w.IdleMs) * time.Millisecond}, true
}

// EncodeLine renders a sample as the helper wire format (with trailing newline).
func EncodeLine(s Sample) []byte {
	b, _ := json.Marshal(wireSample{PID: s.PID, IdleMs: s.Idle.Milliseconds()})
	return append(b, '\n')
}

// ReadSamples reads helper lines from r until EOF or error, passing each valid
// sample that also satisfies validate to onSample. Malformed lines and samples
// that fail validation are dropped silently. A line over MaxLineBytes ends the
// read with an error: the caller should treat that helper as misbehaving.
// validate may be nil.
func ReadSamples(r io.Reader, validate func(pid uint32) bool, onSample func(Sample)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, MaxLineBytes), MaxLineBytes)
	for sc.Scan() {
		s, ok := ParseLine(sc.Bytes())
		if !ok {
			continue
		}
		// pid 0 (no window) has nothing to spoof, so it skips validation.
		if s.PID != 0 && validate != nil && !validate(s.PID) {
			continue
		}
		onSample(s)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

type hubEntry struct {
	sample Sample
	at     time.Time
}

// Hub holds the latest sample per helper and serves only fresh ones.
type Hub struct {
	mu     sync.Mutex
	now    func() time.Time
	latest map[string]hubEntry
}

// NewHub returns an empty Hub.
func NewHub() *Hub {
	return &Hub{now: time.Now, latest: make(map[string]hubEntry)}
}

// Update records the latest sample for a helper, replacing its previous one.
func (h *Hub) Update(helperID string, s Sample) {
	h.mu.Lock()
	h.latest[helperID] = hubEntry{sample: s, at: h.now()}
	h.mu.Unlock()
}

// Remove forgets a helper, e.g. when it exits.
func (h *Hub) Remove(helperID string) {
	h.mu.Lock()
	delete(h.latest, helperID)
	h.mu.Unlock()
}

// Fresh returns each helper's latest sample received within FreshFor, ordered
// by helper ID so callers see a stable sequence.
func (h *Hub) Fresh() []Sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := h.now().Add(-FreshFor)
	ids := make([]string, 0, len(h.latest))
	for id, e := range h.latest {
		if !e.at.Before(cutoff) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]Sample, 0, len(ids))
	for _, id := range ids {
		out = append(out, h.latest[id].sample)
	}
	return out
}

// Connected is the number of helpers currently delivering fresh samples.
func (h *Hub) Connected() int { return len(h.Fresh()) }
