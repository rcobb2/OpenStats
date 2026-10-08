package api

import (
	"strings"
	"testing"
)

// Regression: ListDiscoveredUsers' session-hours column was missed when
// every other range-based query was cut over from raw increase() to the
// openlabstats_report_rollups 15m rollups (see topAppsUsageQuery in
// reports_test.go for the sibling case) — it kept running increase() on the
// raw openlabstats_user_session_seconds_total counter for the Users page's
// default 30d range, on every single page load, not just an explicit report
// request.
func TestSessionHoursByUserQueryUsesRollup(t *testing.T) {
	got := sessionHoursByUserQuery("30d")

	if strings.Contains(got, "increase(openlabstats_user_session_seconds_total") {
		t.Errorf("query must not run increase() on the raw counter for an arbitrary range: %s", got)
	}
	if !strings.Contains(got, "openlabstats:user_session_seconds:rate15m") {
		t.Errorf("query must read the user_session_seconds rollup, got: %s", got)
	}
	if !strings.Contains(got, "sum_over_time") {
		t.Errorf("query must read the recording rule via sum_over_time, not increase(): %s", got)
	}
	if !strings.Contains(got, `{user!=""}`) || !strings.Contains(got, "[30d]") {
		t.Errorf("query must apply the user filter and time range, got: %s", got)
	}
}
