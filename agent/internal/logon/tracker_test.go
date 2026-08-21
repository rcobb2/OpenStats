package logon

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/rcobb/openlabstats-agent/internal/metrics"
)

const host = "TESTHOST"

// fakeEnum returns a scripted session list.
type fakeEnum struct{ sessions []Session }

func (f *fakeEnum) Enumerate() ([]Session, error) { return f.sessions, nil }

func newTestTracker(t *testing.T) (*Tracker, *metrics.Metrics) {
	t.Helper()
	m := metrics.NewForTest()
	// Mirror the real resolver closely enough to matter: lowercase, strip the
	// domain, and drop one service account.
	resolve := func(raw string) (string, bool) {
		s := strings.ToLower(strings.TrimSpace(raw))
		if i := strings.LastIndex(s, `\`); i >= 0 {
			s = s[i+1:]
		}
		if s == "" || s == "zabbix" {
			return "", false
		}
		return s, true
	}
	tr := NewTracker(&fakeEnum{}, m, resolve, host, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return tr, m
}

func counter(t *testing.T, cv *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	c, err := cv.GetMetricWithLabelValues(labels...)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%v): %v", labels, err)
	}
	return testutil.ToFloat64(c)
}

func gauge(t *testing.T, gv *prometheus.GaugeVec, labels ...string) float64 {
	t.Helper()
	g, err := gv.GetMetricWithLabelValues(labels...)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%v): %v", labels, err)
	}
	return testutil.ToFloat64(g)
}

func sessionSet(sessions ...Session) map[SessionKey]Session { return Index(sessions) }

// TestFirstPollAdoptsExistingSessionsWithoutCountingLogins is the whole point of
// this package: an agent restart must not manufacture a logon for every session
// that was already open. That behavior is what pinned every login counter on the
// fleet at exactly 1.
func TestFirstPollAdoptsExistingSessionsWithoutCountingLogins(t *testing.T) {
	tr, m := newTestTracker(t)
	t0 := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	loggedInEarlier := t0.Add(-48 * time.Hour)

	tr.apply(sessionSet(
		Session{ID: "wts-1", RawUser: `COLGATE\pubref`, State: StateActive, LoginTime: loggedInEarlier},
	), t0)

	if got := counter(t, m.UserSessionLogins, "pubref", host); got != 0 {
		t.Errorf("logins after adopting a pre-existing session = %v, want 0", got)
	}
	if got := gauge(t, m.UserSessionActive, "pubref", host); got != 1 {
		t.Errorf("active gauge = %v, want 1", got)
	}
	// Duration comes from the OS logon time, so it survives the restart.
	if got := gauge(t, m.UserSessionDuration, "pubref", host); got != 48*3600 {
		t.Errorf("duration = %v, want %v", got, 48*3600)
	}
}

// TestNewLogonAfterSeedingCounts covers the case the old refcount design could
// not see: a genuine sign-in while another user's processes are already running.
func TestNewLogonAfterSeedingCounts(t *testing.T) {
	tr, m := newTestTracker(t)
	t0 := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)

	existing := Session{ID: "wts-1", RawUser: `COLGATE\pubref`, State: StateActive, LoginTime: t0.Add(-time.Hour)}
	tr.apply(sessionSet(existing), t0)

	arriving := Session{ID: "wts-2", RawUser: `COLGATE\jdoe`, State: StateActive, LoginTime: t0.Add(time.Minute)}
	tr.apply(sessionSet(existing, arriving), t0.Add(time.Minute))

	if got := counter(t, m.UserSessionLogins, "jdoe", host); got != 1 {
		t.Errorf("logins for the arriving user = %v, want 1", got)
	}
	if got := gauge(t, m.UserSessionActive, "jdoe", host); got != 1 {
		t.Errorf("active gauge for arriving user = %v, want 1", got)
	}
	// The incumbent is untouched — no spurious second logon.
	if got := counter(t, m.UserSessionLogins, "pubref", host); got != 0 {
		t.Errorf("logins for the incumbent = %v, want 0", got)
	}
}

func TestSignOutClearsGaugesAndAccruesTime(t *testing.T) {
	tr, m := newTestTracker(t)
	t0 := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)

	s := Session{ID: "wts-3", RawUser: "jdoe", State: StateActive, LoginTime: t0}
	tr.apply(sessionSet(s), t0)
	tr.apply(sessionSet(s), t0.Add(30*time.Minute)) // still signed in
	tr.apply(sessionSet(), t0.Add(45*time.Minute))  // signed out

	if got := gauge(t, m.UserSessionActive, "jdoe", host); got != 0 {
		t.Errorf("active gauge after sign-out = %v, want 0", got)
	}
	if got := gauge(t, m.UserSessionDuration, "jdoe", host); got != 0 {
		t.Errorf("duration gauge after sign-out = %v, want 0", got)
	}
	// 45 minutes elapsed across the polls, all of it while signed in.
	if got := counter(t, m.UserSessionSecondsTotal, "jdoe", host); got != 45*60 {
		t.Errorf("session seconds = %v, want %v", got, 45*60)
	}
}

func TestReLogonCountsASecondTime(t *testing.T) {
	tr, m := newTestTracker(t)
	t0 := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)

	first := Session{ID: "wts-4", RawUser: "jdoe", State: StateActive, LoginTime: t0}
	tr.apply(sessionSet(first), t0)           // adopted, not counted
	tr.apply(sessionSet(), t0.Add(time.Hour)) // signs out
	second := Session{ID: "wts-5", RawUser: "jdoe", State: StateActive, LoginTime: t0.Add(2 * time.Hour)}
	tr.apply(sessionSet(second), t0.Add(2*time.Hour)) // signs back in

	if got := counter(t, m.UserSessionLogins, "jdoe", host); got != 1 {
		t.Errorf("logins after a re-logon = %v, want 1", got)
	}
	if got := gauge(t, m.UserSessionActive, "jdoe", host); got != 1 {
		t.Errorf("active gauge after re-logon = %v, want 1", got)
	}
}

// TestConcurrentSessionsCountTimeOnce guards against double-billing a user who
// holds a console session and an SSH session at once.
func TestConcurrentSessionsCountTimeOnce(t *testing.T) {
	tr, m := newTestTracker(t)
	t0 := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)

	console := Session{ID: "utmpx-console", RawUser: "jdoe", State: StateActive, LoginTime: t0}
	ssh := Session{ID: "utmpx-ttys001", RawUser: "jdoe", State: StateDisconnected, LoginTime: t0}
	tr.apply(sessionSet(console), t0)
	tr.apply(sessionSet(console, ssh), t0.Add(10*time.Minute))
	tr.apply(sessionSet(console, ssh), t0.Add(20*time.Minute))

	if got := counter(t, m.UserSessionSecondsTotal, "jdoe", host); got != 20*60 {
		t.Errorf("session seconds with two concurrent sessions = %v, want %v (wall clock, not doubled)", got, 20*60)
	}
	// Dropping one session leaves the user signed in.
	tr.apply(sessionSet(console), t0.Add(25*time.Minute))
	if got := gauge(t, m.UserSessionActive, "jdoe", host); got != 1 {
		t.Errorf("active gauge after closing one of two sessions = %v, want 1", got)
	}
}

func TestIgnoredUserProducesNoMetrics(t *testing.T) {
	tr, m := newTestTracker(t)
	t0 := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)

	tr.apply(sessionSet(Session{ID: "wts-9", RawUser: "zabbix", State: StateActive, LoginTime: t0}), t0)
	tr.apply(sessionSet(Session{ID: "wts-9", RawUser: "zabbix", State: StateActive, LoginTime: t0}), t0.Add(time.Hour))

	if got := counter(t, m.UserSessionLogins, "zabbix", host); got != 0 {
		t.Errorf("logins for an ignored account = %v, want 0", got)
	}
	if got := counter(t, m.UserSessionSecondsTotal, "zabbix", host); got != 0 {
		t.Errorf("session seconds for an ignored account = %v, want 0", got)
	}
	if users := tr.ActiveUsers(); len(users) != 0 {
		t.Errorf("ActiveUsers() = %v, want empty", users)
	}
}

// TestStateChangeIsNotANewLogon covers RDP detach/reattach and the macOS login
// window: the user never signed out.
func TestStateChangeIsNotANewLogon(t *testing.T) {
	tr, m := newTestTracker(t)
	t0 := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)

	attached := Session{ID: "wts-7", RawUser: "jdoe", State: StateActive, LoginTime: t0}
	tr.apply(sessionSet(attached), t0)
	tr.apply(sessionSet(attached), t0.Add(time.Minute))

	detached := attached
	detached.State = StateDisconnected
	tr.apply(sessionSet(detached), t0.Add(2*time.Minute))

	if got := counter(t, m.UserSessionLogins, "jdoe", host); got != 0 {
		t.Errorf("logins after a detach = %v, want 0 (adopted session, no re-logon)", got)
	}
	if got := gauge(t, m.UserSessionActive, "jdoe", host); got != 1 {
		t.Errorf("active gauge while detached = %v, want 1 (still signed in)", got)
	}
}

func TestDiffAndIndex(t *testing.T) {
	a := Session{ID: "1", RawUser: "alice", State: StateActive}
	b := Session{ID: "2", RawUser: "bob", State: StateActive}

	started, ended, continuing := Diff(sessionSet(a), sessionSet(a, b))
	if len(started) != 1 || started[0].RawUser != "bob" {
		t.Errorf("started = %v, want [bob]", started)
	}
	if len(ended) != 0 {
		t.Errorf("ended = %v, want []", ended)
	}
	if len(continuing) != 1 || continuing[0].RawUser != "alice" {
		t.Errorf("continuing = %v, want [alice]", continuing)
	}

	// Index drops unoccupied stations and folds case in the key.
	idx := Index([]Session{{ID: "3", RawUser: ""}, {ID: "4", RawUser: "  Carol  "}})
	if len(idx) != 1 {
		t.Fatalf("Index kept %d entries, want 1", len(idx))
	}
	if _, ok := idx[SessionKey{ID: "4", RawUser: "carol"}]; !ok {
		t.Errorf("Index key = %v, want normalized to carol", idx)
	}
}
