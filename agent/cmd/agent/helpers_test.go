package main

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rcobb/openlabstats-agent/internal/enrollment"
	"github.com/rcobb/openlabstats-agent/internal/metrics"
	"github.com/rcobb/openlabstats-agent/internal/monitor"
	"github.com/rcobb/openlabstats-agent/internal/normalizer"
	"github.com/rcobb/openlabstats-agent/internal/userid"
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

// ── resolveUser + server-pushed policy ───────────────────────────────────────

// withUserPolicy installs a policy for the duration of one test.
func withUserPolicy(t *testing.T, p *enrollment.UserPolicy) {
	t.Helper()
	t.Cleanup(func() { userPolicy.Set(userid.NewPolicy()) })
	applyUserPolicy(p, discardSlogLogger())
}

func TestResolveUserCanonicalizesDomainAccounts(t *testing.T) {
	cases := map[string]string{
		`COLGATE\jdoe`:     "jdoe",
		"jdoe":             "jdoe",
		"jdoe@colgate.edu": "jdoe",
		"JDoe":             "jdoe",
	}
	for raw, want := range cases {
		got, ok := resolveUser(raw)
		if !ok {
			t.Errorf("resolveUser(%q): expected tracked", raw)
			continue
		}
		if got != want {
			t.Errorf("resolveUser(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestResolveUserAppliesServerIgnoreList(t *testing.T) {
	withUserPolicy(t, &enrollment.UserPolicy{
		StripDomain:    true,
		IgnorePatterns: []string{"zabbix"},
	})
	for _, raw := range []string{"zabbix", `COLGATE\zabbix`} {
		if _, ok := resolveUser(raw); ok {
			t.Errorf("resolveUser(%q): expected ignored", raw)
		}
	}
	if _, ok := resolveUser("alice"); !ok {
		t.Error(`resolveUser("alice"): expected tracked`)
	}
}

func TestUserAliasMergesMacShortname(t *testing.T) {
	withUserPolicy(t, &enrollment.UserPolicy{
		StripDomain: true,
		Aliases:     map[string]string{"jdoe2": "jdoe"},
	})
	got, ok := resolveUser("jdoe2")
	if !ok || got != "jdoe" {
		t.Errorf(`resolveUser("jdoe2") = (%q, %v), want ("jdoe", true)`, got, ok)
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
