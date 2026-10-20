package api

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/rcobb/openlabstats-server/internal/store"
)

func mustParse(t *testing.T, body string) map[string]*dto.MetricFamily {
	t.Helper()
	f, err := parseAgentMetrics([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return f
}

func labelsOf(m *dto.Metric) map[string]string {
	out := map[string]string{}
	for _, lp := range m.Label {
		out[lp.GetName()] = lp.GetValue()
	}
	return out
}

func TestParseAgentMetricsRejectsMalformed(t *testing.T) {
	for _, body := range []string{
		"openlabstats_x{a=\"1\" 5\n",
		"openlabstats_x 1 2 3 4\n",
		"openlabstats_x{a=\"1\",a=\"2\"} 1\n",
	} {
		if _, err := parseAgentMetrics([]byte(body)); err == nil {
			t.Errorf("expected error for %q", body)
		}
	}
}

func TestParseAgentMetricsLimits(t *testing.T) {
	long := strings.Repeat("x", maxLabelValueBytes+1)
	if _, err := parseAgentMetrics([]byte(`openlabstats_x{app="` + long + `"} 1` + "\n")); err == nil {
		t.Error("expected oversized label value to be rejected")
	}
	var sb strings.Builder
	for i := 0; i <= maxSeriesPerFamily; i++ {
		fmt.Fprintf(&sb, "openlabstats_x{i=\"%d\"} 1\n", i)
	}
	if _, err := parseAgentMetrics([]byte(sb.String())); err == nil {
		t.Error("expected family over series cap to be rejected")
	}
}

func TestParseAgentMetricsDropsForeignFamilies(t *testing.T) {
	f := mustParse(t, "go_goroutines 5\nopenlabstats_ok 1\nprocess_cpu 2\n")
	if len(f) != 1 || f["openlabstats_ok"] == nil {
		t.Fatalf("expected only openlabstats_ok, got %v", f)
	}
}

func TestStampServerLabelsOverwritesSpoofedLabels(t *testing.T) {
	f := mustParse(t, `openlabstats_x{hostname="real",lab="Evil",building="B",room="R",app="a"} 1`+"\n"+
		`openlabstats_x{hostname="victim",app="a"} 5`+"\n")
	if n := stampServerLabels(f, "REAL", map[string]string{"lab": "Lab1"}); n != 1 {
		t.Fatalf("expected the victim-hostname series to be dropped, dropped=%d", n)
	}
	if len(f["openlabstats_x"].Metric) != 1 {
		t.Fatalf("expected 1 series left, got %d", len(f["openlabstats_x"].Metric))
	}
	l := labelsOf(f["openlabstats_x"].Metric[0])
	if l["hostname"] != "REAL" || l["lab"] != "Lab1" || l["app"] != "a" {
		t.Errorf("unexpected labels %v", l)
	}
	if _, ok := l["building"]; ok {
		t.Errorf("agent-supplied building should be stripped, got %v", l)
	}
	if _, ok := l["room"]; ok {
		t.Errorf("agent-supplied room should be stripped, got %v", l)
	}
}

func TestStampServerLabelsStripsLabWhenServerHasNone(t *testing.T) {
	f := mustParse(t, `openlabstats_x{lab="Evil"} 1`+"\n")
	stampServerLabels(f, "H", nil)
	if _, ok := labelsOf(f["openlabstats_x"].Metric[0])["lab"]; ok {
		t.Error("lab should be stripped")
	}
}

// A Mac renamed while the agent runs exposes series under both names; the
// push must still be accepted, keeping only the current hostname's series.
func TestStampServerLabelsDropsRenamedHostSeries(t *testing.T) {
	f := mustParse(t, "openlabstats_x{hostname=\"Litt113\",user=\"u\"} 1\nopenlabstats_x{hostname=\"Ryan113\",user=\"u\"} 2\nopenlabstats_y{hostname=\"Ryan113\"} 3\n")
	if n := stampServerLabels(f, "Litt113", nil); n != 2 {
		t.Fatalf("dropped=%d, want 2", n)
	}
	if ms := f["openlabstats_x"].Metric; len(ms) != 1 || ms[0].GetCounter().GetValue()+ms[0].GetUntyped().GetValue()+ms[0].GetGauge().GetValue() != 1 {
		t.Fatalf("expected only the current host's series, got %v", ms)
	}
	if _, ok := f["openlabstats_y"]; ok {
		t.Error("a family left empty should be removed")
	}
}

func TestMalformedPushKeepsPreviousSnapshot(t *testing.T) {
	ms := &MetricsStore{staleness: 5 * time.Minute, snapshots: map[string]agentSnapshot{}}
	good := mustParse(t, "openlabstats_x 1\n")
	if n := stampServerLabels(good, "H", nil); n != 0 {
		t.Fatalf("dropped %d", n)
	}
	ms.Set("a1", good)
	// The handler parses before touching the store; a parse failure returns early.
	if _, err := parseAgentMetrics([]byte("openlabstats_x{broken 1\n")); err == nil {
		t.Fatal("expected parse error")
	}
	got := ms.GetAll()
	if len(got) != 1 || got[0]["openlabstats_x"] == nil {
		t.Errorf("previous snapshot lost: %v", got)
	}
}

func TestPushAgentMetricsRequiresAgentID(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.PushAgentMetrics(rec, httptest.NewRequest(http.MethodPost, "/api/v1/agents/metrics", strings.NewReader("x 1\n")))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestApplyMappingsToFamiliesDropsEmptyFamilies(t *testing.T) {
	f := mustParse(t, `openlabstats_x{exe="a.exe",app="a"} 1`+"\n")
	applyMappingsToFamilies(f, map[string]*store.SoftwareMapping{"a.exe": {Ignored: true}})
	if len(f) != 0 {
		t.Errorf("expected empty family removed, got %v", f)
	}
}

func TestServeAgentMetricsMergesFamiliesWithSingleHelpType(t *testing.T) {
	mk := func(host string) map[string]*dto.MetricFamily {
		f := mustParse(t, "# HELP openlabstats_x Test metric.\n# TYPE openlabstats_x counter\nopenlabstats_x{app=\"a\"} 1\n")
		if n := stampServerLabels(f, host, nil); n != 0 {
			t.Fatalf("dropped %d", n)
		}
		return f
	}
	ms := &MetricsStore{staleness: 5 * time.Minute, snapshots: map[string]agentSnapshot{}}
	ms.Set("a1", mk("H1"))
	ms.Set("a2", mk("H2"))

	s := &Server{metricsStore: ms}
	rec := httptest.NewRecorder()
	s.ServeAgentMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics/agents", nil))
	out := rec.Body.String()

	if n := strings.Count(out, "# TYPE openlabstats_x"); n != 1 {
		t.Errorf("want 1 TYPE line, got %d:\n%s", n, out)
	}
	if n := strings.Count(out, "# HELP openlabstats_x"); n > 1 {
		t.Errorf("want at most 1 HELP line, got %d", n)
	}
	parser := expfmt.NewTextParser(model.LegacyValidation)
	fams, err := parser.TextToMetricFamilies(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("output does not parse: %v\n%s", err, out)
	}
	got := len(fams["openlabstats_x"].Metric)
	if got != 2 {
		t.Errorf("want 2 series, got %d:\n%s", got, out)
	}
}

func TestMergeAgentFamiliesDropsConflictingType(t *testing.T) {
	a := mustParse(t, "# TYPE openlabstats_x counter\nopenlabstats_x 1\n")
	b := mustParse(t, "# TYPE openlabstats_x gauge\nopenlabstats_x{h=\"2\"} 3\n")
	merged := mergeAgentFamilies([]map[string]*dto.MetricFamily{a, b}, nil)
	if len(merged) != 1 || len(merged[0].Metric) != 1 || merged[0].GetType() != dto.MetricType_COUNTER {
		t.Errorf("conflicting family should be dropped, got %v", merged)
	}
}
