package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rcobb/openlabstats-server/internal/config"
	"github.com/rcobb/openlabstats-server/internal/store"
	"github.com/rcobb/openlabstats-server/internal/userid"
)

// The regression this guards against: genchem had 1 login and 191.87 hours
// of accrued session time over 30 days (a shared/kiosk-style account that
// rarely signs all the way out), producing an "average session" of 11,506
// minutes — a number no one experienced as one sitting. Below
// minLoginsForAvgSession the sample is too small to average meaningfully, so
// the user should be omitted from this report entirely rather than shown
// with a distorted number.
func TestComputeAvgSessionMinutesOmitsLowSampleUsers(t *testing.T) {
	seconds := map[string]float64{
		"genchem": 690732,  // 191.87 hours
		"mabrown": 1205388, // 334.83 hours
		"darias":  21600,   // 6 hours
	}
	logins := map[string]float64{
		"genchem": 1,
		"mabrown": 3,
		"darias":  6,
	}

	got := computeAvgSessionMinutes(seconds, logins, 3)

	if _, ok := got["genchem"]; ok {
		t.Errorf("genchem has only 1 login and should be omitted, got %v", got["genchem"])
	}
	if v, ok := got["mabrown"]; !ok {
		t.Error("mabrown has exactly the minimum login count and should be included")
	} else if want := 1205388.0 / 3 / 60; v != want {
		t.Errorf("mabrown avg = %v, want %v", v, want)
	}
	if v, ok := got["darias"]; !ok {
		t.Error("darias has well above the minimum and should be included")
	} else if want := 21600.0 / 6 / 60; v != want {
		t.Errorf("darias avg = %v, want %v", v, want)
	}
}

// A user with an ongoing session but no fresh login inside the window has a
// zero denominator (absent from the logins map entirely) and must be
// omitted, not reported as infinite.
func TestComputeAvgSessionMinutesOmitsZeroLogins(t *testing.T) {
	seconds := map[string]float64{"nologinuser": 3600}
	logins := map[string]float64{} // no entry at all

	got := computeAvgSessionMinutes(seconds, logins, 3)

	if _, ok := got["nologinuser"]; ok {
		t.Error("user with no logins in the window should be omitted, not reported as infinite")
	}
}

// hostnameMatchesLab is the sole place login/session reports enforce the lab
// filter, since those metrics carry no native Prometheus lab label. An empty
// filter must pass everything through unfiltered, an unmapped hostname must
// fall into "Unassigned" rather than matching every filter, and matching must
// be case-insensitive like the rest of the lab-name comparisons in this file.
func TestHostnameMatchesLab(t *testing.T) {
	hostnameToLab := map[string]string{
		"serp-02586m": "Serpent Hall",
	}
	tests := []struct {
		name      string
		hostname  string
		labFilter string
		want      bool
	}{
		{"empty filter matches everything", "serp-02586m", "", true},
		{"matching lab (case-insensitive)", "SERP-02586M", "serpent hall", true},
		{"non-matching lab", "serp-02586m", "Other Lab", false},
		{"unmapped hostname falls into Unassigned", "unknown-host", "Unassigned", true},
		{"unmapped hostname does not match a real lab", "unknown-host", "Serpent Hall", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hostnameMatchesLab(hostnameToLab, tt.hostname, tt.labFilter); got != tt.want {
				t.Errorf("hostnameMatchesLab(%q, %q) = %v, want %v", tt.hostname, tt.labFilter, got, tt.want)
			}
		})
	}
}

// This is the regression the per-lab selector was silently missing: session
// and login metrics have no lab label in Prometheus, so filtering must happen
// in Go against the DB-based hostname->lab map *before* the per-user merge —
// otherwise a lab filter on these panels does nothing and every lab shows
// fleet-wide totals.
func TestSumByCanonicalUserFiltersByLab(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "vector",
				"result": []map[string]interface{}{
					{"metric": map[string]string{"user": "alice", "hostname": "serp-02586m"}, "value": []interface{}{1, "10"}},
					{"metric": map[string]string{"user": "bob", "hostname": "case-99898w"}, "value": []interface{}{1, "20"}},
				},
			},
		})
	}))
	defer ts.Close()

	s := &Server{
		cfg:        &config.Config{Prom: config.PromConfig{URL: ts.URL}},
		logger:     slog.Default(),
		promClient: &http.Client{},
	}
	hostnameToLab := map[string]string{
		"serp-02586m": "Serpent Hall",
		"case-99898w": "Case Lab",
	}
	policy := userid.NewPolicy()

	totals, err := s.sumByCanonicalUser(context.Background(), "irrelevant-query", 0, policy, hostnameToLab, "Serpent Hall")
	if err != nil {
		t.Fatalf("sumByCanonicalUser() error = %v", err)
	}
	if _, ok := totals["bob"]; ok {
		t.Error("bob's host is in Case Lab; a Serpent Hall filter should have dropped it")
	}
	if got := totals["alice"]; got != 10 {
		t.Errorf("alice's host is in Serpent Hall and should pass the filter, got total = %v, want 10", got)
	}

	unfiltered, err := s.sumByCanonicalUser(context.Background(), "irrelevant-query", 0, policy, hostnameToLab, "")
	if err != nil {
		t.Fatalf("sumByCanonicalUser() error = %v", err)
	}
	if len(unfiltered) != 2 {
		t.Errorf("empty lab filter should keep every user, got %d users, want 2", len(unfiltered))
	}
}

// ReportTopDevicesBySessionCount used to rank with a PromQL topk() over every
// lab at once; a lab filter applied after that would have already lost to
// higher-count hostnames from other labs. rankHostnamesByValue moved the
// filter before the ranking — this pins that order.
func TestRankHostnamesByValueFiltersBeforeRanking(t *testing.T) {
	raw := promQueryInstantResult{}
	raw.Data.Result = []struct {
		Metric map[string]string `json:"metric"`
		Value  []interface{}     `json:"value"`
	}{
		{Metric: map[string]string{"hostname": "case-99898w"}, Value: []interface{}{1, "100"}}, // Case Lab, highest count
		{Metric: map[string]string{"hostname": "serp-02586m"}, Value: []interface{}{1, "58"}},  // Serpent Hall
		{Metric: map[string]string{"hostname": "serp-99999m"}, Value: []interface{}{1, "10"}},  // Serpent Hall
	}
	hostnameToLab := map[string]string{
		"case-99898w": "Case Lab",
		"serp-02586m": "Serpent Hall",
		"serp-99999m": "Serpent Hall",
	}

	got := rankHostnamesByValue(raw, hostnameToLab, "Serpent Hall", 10)

	if len(got.Data.Result) != 2 {
		t.Fatalf("expected 2 Serpent Hall hosts, got %d: %+v", len(got.Data.Result), got.Data.Result)
	}
	for _, entry := range got.Data.Result {
		if entry.Metric["hostname"] == "case-99898w" {
			t.Error("case-99898w is in Case Lab and should have been filtered before ranking, not just ranked out")
		}
	}
	if got.Data.Result[0].Metric["hostname"] != "serp-02586m" {
		t.Errorf("expected serp-02586m (58) ranked above serp-99999m (10), got order %+v", got.Data.Result)
	}

	limited := rankHostnamesByValue(raw, hostnameToLab, "Serpent Hall", 1)
	if len(limited.Data.Result) != 1 {
		t.Errorf("limit=1 should keep exactly 1 result, got %d", len(limited.Data.Result))
	}
}

// The regression this guards against: a mapping edit renames an app's
// category, but Prometheus keeps the old (app, category) series alive until
// it expires. Both series pass the whitelist and both get counted as
// separate "top 10" slots even though the frontend later merges them back
// into one bar for display — so a panel labeled "Top 10" could render as few
// as 5-8 bars. Filtering has to collapse by app name before the limit
// truncation, not after, or the merged-down count silently falls short.
func TestMergeAppNameDuplicatesSumsAcrossCategories(t *testing.T) {
	raw := []struct {
		Metric map[string]string `json:"metric"`
		Value  []interface{}     `json:"value"`
	}{
		{Metric: map[string]string{"app": "Adobe Creative Cloud", "category": "Creative"}, Value: []interface{}{1, "15"}},
		{Metric: map[string]string{"app": "adobe creative cloud", "category": "Utility"}, Value: []interface{}{1, "12"}}, // stale pre-rename series
		{Metric: map[string]string{"app": "Google Chrome", "category": "Browser"}, Value: []interface{}{1, "71"}},
	}

	got := mergeAppNameDuplicates(raw)

	if len(got) != 2 {
		t.Fatalf("expected 2 unique apps after merge, got %d: %+v", len(got), got)
	}
	byApp := make(map[string]struct {
		Metric map[string]string `json:"metric"`
		Value  []interface{}     `json:"value"`
	})
	for _, r := range got {
		byApp[strings.ToLower(r.Metric["app"])] = r
	}
	cc, ok := byApp["adobe creative cloud"]
	if !ok {
		t.Fatal("adobe creative cloud missing from merged result")
	}
	if v := cc.Value[1].(string); v != "27" {
		t.Errorf("adobe creative cloud total = %v, want 27 (15+12 summed across category duplicates)", v)
	}
	if cc.Metric["category"] != "Creative" {
		t.Errorf("expected the surviving row to keep the larger-value duplicate's category, got %q", cc.Metric["category"])
	}
}

// The elevations report deliberately passes a nil whitelist to
// queryAndRespondFiltered: elevations usually come from unmapped one-off
// executables (setup.exe-style installers), and hiding unmapped apps would
// hide exactly what the report exists to surface. Guard both directions —
// nil passes everything through, a populated whitelist filters.
func TestQueryAndRespondFilteredNilWhitelistPassesUnmappedApps(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "vector",
				"result": []map[string]interface{}{
					{"metric": map[string]string{"app": "setup.exe", "category": ""}, "value": []interface{}{1, "4"}},
					{"metric": map[string]string{"app": "MATLAB", "category": "Math"}, "value": []interface{}{1, "2"}},
				},
			},
		})
	}))
	defer ts.Close()

	s := &Server{
		cfg:        &config.Config{Prom: config.PromConfig{URL: ts.URL}},
		logger:     slog.Default(),
		promClient: &http.Client{},
	}

	run := func(allowed map[string]bool) []string {
		t.Helper()
		rec := httptest.NewRecorder()
		s.queryAndRespondFiltered(rec, "irrelevant-query", "", 0, allowed, 10, false)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var out promQueryInstantResult
		if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		apps := make([]string, 0, len(out.Data.Result))
		for _, r := range out.Data.Result {
			apps = append(apps, r.Metric["app"])
		}
		return apps
	}

	nilApps := run(nil)
	if len(nilApps) != 2 {
		t.Errorf("nil whitelist should pass every app through, got %v", nilApps)
	}

	filtered := run(map[string]bool{"matlab": true})
	if len(filtered) != 1 || filtered[0] != "MATLAB" {
		t.Errorf("whitelist should keep only mapped apps, got %v", filtered)
	}
}

// The underutilized "view all" must include known apps that never launched in
// the window (count 0), summed same-name launched rows, whitelist-filtered, and
// ordered 0-launch-first. Mirrors mergeBottomAppsWithZeros' contract.
func TestMergeBottomAppsWithZeros(t *testing.T) {
	raw := promQueryInstantResult{}
	raw.Data.Result = []struct {
		Metric map[string]string `json:"metric"`
		Value  []interface{}     `json:"value"`
	}{
		{Metric: map[string]string{"app": "Firefox", "category": "Browser"}, Value: []interface{}{1, "3"}},
		{Metric: map[string]string{"app": "Keynote", "category": "Business"}, Value: []interface{}{1, "1"}},
		{Metric: map[string]string{"app": "NotMapped", "category": "X"}, Value: []interface{}{1, "9"}}, // dropped by whitelist
	}
	allowed := map[string]bool{"firefox": true, "keynote": true, "texmaker": true, "stata": true}
	mappings := []store.SoftwareMapping{
		{DisplayName: "Firefox"},                  // launched → not re-added
		{DisplayName: "texmaker", Category: "Ed"}, // never launched → 0
		{DisplayName: "Stata"},                    // never launched → 0
		{DisplayName: "Ignored App", Ignored: true},
		{DisplayName: "texmaker"}, // dup display name → counted once
	}

	got := mergeBottomAppsWithZeros(raw, allowed, mappings, 10)
	res := got.Data.Result

	// NotMapped excluded; Firefox+Keynote+texmaker+Stata = 4.
	if len(res) != 4 {
		t.Fatalf("expected 4 rows, got %d: %+v", len(res), res)
	}
	// Ascending, 0-launch first, ties by name: Stata(0), texmaker(0), Keynote(1), Firefox(3).
	wantOrder := []struct {
		app string
		val string
	}{{"Stata", "0"}, {"texmaker", "0"}, {"Keynote", "1"}, {"Firefox", "3"}}
	for i, w := range wantOrder {
		gotApp := res[i].Metric["app"]
		gotVal := res[i].Value[1].(string)
		if gotApp != w.app || gotVal != w.val {
			t.Errorf("row %d = (%s, %s), want (%s, %s)", i, gotApp, gotVal, w.app, w.val)
		}
	}
	// Whitelisted-out app absent.
	for _, r := range res {
		if r.Metric["app"] == "NotMapped" {
			t.Error("NotMapped should have been dropped by the whitelist")
		}
	}
}

// With a limit smaller than the candidate set, the most-underutilized
// (0-launch) apps must win the truncation.
func TestMergeBottomAppsWithZerosLimit(t *testing.T) {
	raw := promQueryInstantResult{}
	raw.Data.Result = []struct {
		Metric map[string]string `json:"metric"`
		Value  []interface{}     `json:"value"`
	}{
		{Metric: map[string]string{"app": "Busy"}, Value: []interface{}{1, "50"}},
	}
	mappings := []store.SoftwareMapping{{DisplayName: "Za"}, {DisplayName: "Zb"}, {DisplayName: "Busy"}}
	got := mergeBottomAppsWithZeros(raw, nil, mappings, 2)
	if len(got.Data.Result) != 2 {
		t.Fatalf("limit=2 should return 2 rows, got %d", len(got.Data.Result))
	}
	for _, r := range got.Data.Result {
		if r.Value[1].(string) != "0" {
			t.Errorf("expected only 0-launch apps within limit, got %s=%s", r.Metric["app"], r.Value[1])
		}
	}
}
