package main

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rcobb/openlabstats-agent/internal/metrics"
	"github.com/rcobb/openlabstats-agent/internal/monitor"
	"github.com/rcobb/openlabstats-agent/internal/normalizer"
)

func discardSlogLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// counterValue reads the current value of a prometheus.CounterVec for a label set.
func counterValue(t *testing.T, cv *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	c, err := cv.GetMetricWithLabelValues(labels...)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%v): %v", labels, err)
	}
	return testutil.ToFloat64(c)
}

// gaugeValue reads the current value of a prometheus.GaugeVec for a label set.
func gaugeValue(t *testing.T, gv *prometheus.GaugeVec, labels ...string) float64 {
	t.Helper()
	g, err := gv.GetMetricWithLabelValues(labels...)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%v): %v", labels, err)
	}
	return testutil.ToFloat64(g)
}

func newTestSessionManager(t *testing.T) (*userSessionManager, *metrics.Metrics) {
	t.Helper()
	m := metrics.NewForTest()
	usm := newUserSessionManager(m, discardSlogLogger())
	return usm, m
}

// ── isValidUser ───────────────────────────────────────────────────────────────

func TestIsValidUser(t *testing.T) {
	valid := []string{
		"alice",
		"bob.smith",
		"student01",
		"rcobb@colgate.edu",
	}
	for _, u := range valid {
		if !isValidUser(u) {
			t.Errorf("isValidUser(%q) = false, want true", u)
		}
	}
}

func TestIsValidUserFiltersSystemAccounts(t *testing.T) {
	invalid := []string{
		"",
		"root",
		"daemon",
		"nobody",
		"wheel",
		"_spotlight",
		"_www",
		"_mdnsresponder",
		"SYSTEM",
		"LOCAL SERVICE",
		"NETWORK SERVICE",
		"NT AUTHORITY\\SYSTEM",
		"NT SERVICE\\WdiServiceHost",
		"MACHINE$",
		"notepad.exe",
		"x", // too short
	}
	for _, u := range invalid {
		if isValidUser(u) {
			t.Errorf("isValidUser(%q) = true, want false (should be filtered)", u)
		}
	}
}

// ── userSessionManager: login tracking ───────────────────────────────────────

// TestUserSessionLoginFired verifies the login counter increments on first process.
func TestUserSessionLoginFired(t *testing.T) {
	usm, m := newTestSessionManager(t)
	host := metrics.Hostname()

	usm.onProcessStart("alice", 1001)

	if got := counterValue(t, m.UserSessionLogins, "alice", host); got != 1 {
		t.Errorf("logins: got %v, want 1", got)
	}
	if got := gaugeValue(t, m.UserSessionActive, "alice", host); got != 1 {
		t.Errorf("session active: got %v, want 1", got)
	}
}

// TestUserSessionSecondProcessNoExtraLogin verifies a second process for the
// same user does NOT fire an additional login.
func TestUserSessionSecondProcessNoExtraLogin(t *testing.T) {
	usm, m := newTestSessionManager(t)
	host := metrics.Hostname()

	usm.onProcessStart("bob", 2001)
	usm.onProcessStart("bob", 2002)

	if got := counterValue(t, m.UserSessionLogins, "bob", host); got != 1 {
		t.Errorf("logins: got %v, want 1 (two processes, one session)", got)
	}
}

// TestUserSessionLogoffFiredOnLastExit verifies session-end metrics fire only
// when the last process for a user exits.
func TestUserSessionLogoffFiredOnLastExit(t *testing.T) {
	usm, m := newTestSessionManager(t)
	host := metrics.Hostname()

	usm.onProcessStart("carol", 3001)
	usm.onProcessStart("carol", 3002)

	// First exit: session still active.
	usm.onProcessStop("carol", 3001)
	if got := gaugeValue(t, m.UserSessionActive, "carol", host); got != 1 {
		t.Errorf("expected session still active after first exit, got %v", got)
	}

	// Second exit: session ends.
	time.Sleep(5 * time.Millisecond)
	usm.onProcessStop("carol", 3002)

	if got := gaugeValue(t, m.UserSessionActive, "carol", host); got != 0 {
		t.Errorf("expected session inactive after last exit, got %v", got)
	}
	if got := counterValue(t, m.UserSessionSecondsTotal, "carol", host); got <= 0 {
		t.Errorf("expected positive session duration recorded, got %v", got)
	}
}

// TestUserSessionRelogin verifies a user can open a second session after
// their first one ends.
func TestUserSessionRelogin(t *testing.T) {
	usm, m := newTestSessionManager(t)
	host := metrics.Hostname()

	usm.onProcessStart("dave", 4001)
	usm.onProcessStop("dave", 4001)

	if got := counterValue(t, m.UserSessionLogins, "dave", host); got != 1 {
		t.Errorf("after first session: logins=%v, want 1", got)
	}

	usm.onProcessStart("dave", 4002)

	if got := counterValue(t, m.UserSessionLogins, "dave", host); got != 2 {
		t.Errorf("after re-login: logins=%v, want 2", got)
	}
	if got := gaugeValue(t, m.UserSessionActive, "dave", host); got != 1 {
		t.Errorf("after re-login: active=%v, want 1", got)
	}
}

// TestUserSessionStopUnknownUserIsNoop verifies stopping an untracked user
// does not panic or affect other users' state.
func TestUserSessionStopUnknownUserIsNoop(t *testing.T) {
	usm, m := newTestSessionManager(t)
	host := metrics.Hostname()

	usm.onProcessStart("eve", 5001)
	usm.onProcessStop("ghost", 9999) // not tracked — should be a no-op

	if got := gaugeValue(t, m.UserSessionActive, "eve", host); got != 1 {
		t.Errorf("eve's session should be unaffected: active=%v", got)
	}
}

// ── Usage accumulation across checkpoints ────────────────────────────────────

// TestUsageAccumulatesAcrossCheckpoints simulates the runCheckpointLoop pattern:
// starts a process, calls CheckpointActive twice, and verifies that usage
// counters grow on each call.
func TestUsageAccumulatesAcrossCheckpoints(t *testing.T) {
	m := metrics.NewForTest()
	logger := discardSlogLogger()
	host := metrics.Hostname()

	tr := monitor.NewTracker(logger)
	norm := normalizer.NewNormalizer(nil, nil, logger)

	const exeName = "matlab.exe"
	const exePath = "/Applications/MATLAB.app/Contents/MacOS/matlab"
	const user = "frank"

	tr.OnProcessStart(6001, 0, exeName, exePath, user, "")

	// Derive the label set exactly as runCheckpointLoop does.
	info := norm.Resolve(exeName, exePath)
	labels := []string{info.DisplayName, exeName, info.Category, user, host}

	applyCheckpoint := func() {
		time.Sleep(20 * time.Millisecond)
		for _, s := range tr.CheckpointActive() {
			if isValidUser(s.User) {
				inf := norm.Resolve(s.ExeName, s.ExePath)
				l := []string{inf.DisplayName, s.ExeName, inf.Category, s.User, host}
				m.AppUsageSeconds.WithLabelValues(l...).Add(s.CheckpointDelta.Seconds())
			}
		}
	}

	applyCheckpoint()
	usage1 := counterValue(t, m.AppUsageSeconds, labels...)
	if usage1 <= 0 {
		t.Errorf("expected positive usage after first checkpoint, got %v", usage1)
	}

	applyCheckpoint()
	usage2 := counterValue(t, m.AppUsageSeconds, labels...)
	if usage2 <= usage1 {
		t.Errorf("expected usage to grow: checkpoint1=%v checkpoint2=%v", usage1, usage2)
	}
}

// TestForegroundAccumulatesIntoMetrics verifies that foreground time tracked by
// the Tracker is correctly carried through to the Prometheus counter.
func TestForegroundAccumulatesIntoMetrics(t *testing.T) {
	m := metrics.NewForTest()
	logger := discardSlogLogger()
	host := metrics.Hostname()

	tr := monitor.NewTracker(logger)
	norm := normalizer.NewNormalizer(nil, nil, logger)

	const exeName = "vscode.exe"
	const exePath = "/Applications/Visual Studio Code.app/Contents/MacOS/Electron"
	const user = "grace"

	tr.OnProcessStart(7001, 0, exeName, exePath, user, "")
	tr.IncrementForeground(7001, 5*time.Second)

	info := norm.Resolve(exeName, exePath)
	labels := []string{info.DisplayName, exeName, info.Category, user, host}

	for _, s := range tr.CheckpointActive() {
		if isValidUser(s.User) {
			inf := norm.Resolve(s.ExeName, s.ExePath)
			l := []string{inf.DisplayName, s.ExeName, inf.Category, s.User, host}
			m.AppForegroundSeconds.WithLabelValues(l...).Add(s.ForegroundDelta.Seconds())
		}
	}

	fg := counterValue(t, m.AppForegroundSeconds, labels...)
	if fg != 5 {
		t.Errorf("expected foreground=5s, got %v", fg)
	}
}

// TestLaunchCounterIncrementsOnStop verifies that the launch counter increments
// when a process session ends (mimicking the OnStop callback behaviour).
func TestLaunchCounterIncrementsOnStop(t *testing.T) {
	m := metrics.NewForTest()
	logger := discardSlogLogger()
	host := metrics.Hostname()

	tr := monitor.NewTracker(logger)
	norm := normalizer.NewNormalizer(nil, nil, logger)

	tr.OnProcessStart(8001, 0, "photoshop.exe", "/Applications/Adobe Photoshop.app/Contents/MacOS/Photoshop", "henry", "")
	time.Sleep(5 * time.Millisecond)
	sess := tr.OnProcessStop(8001)
	if sess == nil {
		t.Fatal("expected a session on stop")
	}

	info := norm.Resolve(sess.ExeName, sess.ExePath)
	labels := []string{info.DisplayName, sess.ExeName, info.Category, sess.User, host}
	m.AppLaunches.WithLabelValues(labels...).Inc()
	m.AppUsageSeconds.WithLabelValues(labels...).Add(sess.CheckpointDelta.Seconds())

	if got := counterValue(t, m.AppLaunches, labels...); got != 1 {
		t.Errorf("launches: got %v, want 1", got)
	}
	if got := counterValue(t, m.AppUsageSeconds, labels...); got <= 0 {
		t.Errorf("usage: got %v, want >0", got)
	}
}
