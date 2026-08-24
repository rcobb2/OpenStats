package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rcobb/openlabstats-server/internal/userid"
)

// promQueryInstantResult represents an instant query response.
type promQueryInstantResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// ReportUsageByLab godoc
// @Summary      Usage breakdown by lab
// @Description  Returns total app usage hours grouped by lab over the given time range.
// @Tags         reports
// @Produce      json
// @Param        range    query  string  false  "Time range (e.g. 24h, 7d)"  default(24h)
// @Param        hostname query  string  false  "Filter to a specific machine hostname"
// @Param        lab      query  string  false  "Filter to a specific lab name"
// @Param        start    query  string  false  "Custom range start (unix or RFC3339)"
// @Param        end      query  string  false  "Custom range end (unix or RFC3339)"
// @Param        format   query  string  false  "Output format: json or csv"  default(json)
// @Success      200  {object}  map[string]interface{}
// @Failure      502  {object}  map[string]string
// @Router       /api/v1/reports/usage-by-lab [get]
func (s *Server) ReportUsageByLab(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	timeRange := safeTimeRange(q.Get("range"), "24h")
	atTime := int64(0)
	if dur, end, ok := parseCustomTimeRange(q.Get("start"), q.Get("end")); ok {
		timeRange = dur
		atTime = end
	}

	// Build hostname→labName from current DB assignments so that reassigned
	// machines are immediately reflected without waiting for Prometheus labels
	// to rotate out.
	hostnameToLab, err := s.buildHostnameLabMap(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build lab map")
		return
	}

	// Query by hostname — ignore the Prometheus lab label entirely.
	hf := s.buildLabelFilters(r.Context(), q.Get("hostname"), "")
	query := fmt.Sprintf(
		`sum by (hostname, app) (increase(openlabstats_app_usage_seconds_total%s[%s])) > 0`,
		hf, timeRange,
	)

	promURL := fmt.Sprintf("%s/api/v1/query?query=%s", s.cfg.Prom.URL, url.QueryEscape(query))
	if atTime > 0 {
		promURL += fmt.Sprintf("&time=%d", atTime)
	}
	resp, err := s.promClient.Get(promURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to reach Prometheus")
		return
	}
	defer resp.Body.Close()

	var raw promQueryInstantResult
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadGateway, "failed to parse Prometheus response")
		return
	}

	allowedApps := s.allowedAppSet(ctx)
	labFilter := q.Get("lab")

	// Aggregate (lab, app) → total seconds using current DB lab assignments.
	type labAppKey struct{ lab, app string }
	agg := make(map[labAppKey]float64)
	for _, entry := range raw.Data.Result {
		app := entry.Metric["app"]
		if len(allowedApps) > 0 && !allowedApps[strings.ToLower(app)] {
			continue
		}
		labName := hostnameToLab[strings.ToLower(entry.Metric["hostname"])]
		if labName == "" {
			labName = "Unassigned"
		}
		if labFilter != "" && !strings.EqualFold(labName, labFilter) {
			continue
		}
		if len(entry.Value) < 2 {
			continue
		}
		v, ok := entry.Value[1].(string)
		if !ok {
			continue
		}
		val, _ := strconv.ParseFloat(v, 64)
		if val > 0 {
			agg[labAppKey{labName, app}] += val
		}
	}

	// Reconstruct as Prometheus vector format so the existing frontend works unchanged.
	now := time.Now().Unix()
	var result promQueryInstantResult
	result.Status = "success"
	result.Data.ResultType = "vector"
	for k, v := range agg {
		result.Data.Result = append(result.Data.Result, struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		}{
			Metric: map[string]string{"lab": k.lab, "app": k.app},
			Value:  []interface{}{now, fmt.Sprintf("%g", v)},
		})
	}

	if q.Get("format") == "csv" {
		s.writeCSV(w, result.Data.Result)
	} else {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

// buildHostnameLabMap returns a lowercased-hostname → lab-name map using current DB
// lab assignments. Unassigned machines map to "Unassigned".
func (s *Server) buildHostnameLabMap(ctx context.Context) (map[string]string, error) {
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	labs, err := s.store.ListLabs(ctx)
	if err != nil {
		return nil, err
	}
	labByID := make(map[string]string, len(labs))
	for _, l := range labs {
		labByID[l.ID] = l.Name
	}
	m := make(map[string]string, len(agents))
	for _, a := range agents {
		labName := "Unassigned"
		if a.LabID != nil && *a.LabID != "" {
			if name, ok := labByID[*a.LabID]; ok {
				labName = name
			}
		}
		m[strings.ToLower(a.Hostname)] = labName
	}
	return m, nil
}

// ReportActiveUsers godoc
// @Summary      Currently active users
// @Description  Returns users with active sessions right now.
// @Tags         reports
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Router       /api/v1/reports/active-users [get]
func (s *Server) ReportActiveUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	policy := s.userPolicy(ctx)
	query := fmt.Sprintf(`openlabstats_user_session_active%s == 1`, s.buildUserSessionFilters(ctx, ""))

	raw, err := s.fetchInstantVector(ctx, query, 0)
	if err != nil {
		s.logger.Error("prometheus query failed", "error", err, "query", query)
		writeError(w, http.StatusBadGateway, "failed to reach Prometheus")
		return
	}

	// Collapse each raw username to its canonical identity so one person signed
	// in as COLGATE\jdoe and as jdoe counts once per machine.
	type key struct{ user, hostname string }
	seen := make(map[key]bool)
	result := newVectorResult()
	for _, entry := range raw.Data.Result {
		canonical, ignored := policy.Resolve(entry.Metric["user"])
		if ignored || canonical == "" {
			continue
		}
		k := key{canonical, entry.Metric["hostname"]}
		if seen[k] {
			continue
		}
		seen[k] = true
		appendVectorSample(&result, map[string]string{"user": canonical, "hostname": k.hostname}, 1)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// SummaryResponse holds the fleet summary stats.
type SummaryResponse struct {
	TotalAgents   int `json:"totalAgents"`
	OnlineAgents  int `json:"onlineAgents"`
	TotalLabs     int `json:"totalLabs"`
	TotalMappings int `json:"totalMappings"`
}

// ReportSummary godoc
// @Summary      Fleet summary
// @Description  Returns high-level fleet stats: total agents, online count, labs, mappings.
// @Tags         reports
// @Produce      json
// @Success      200  {object}  SummaryResponse
// @Router       /api/v1/reports/summary [get]
func (s *Server) ReportSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get agents")
		return
	}

	online := 0
	for _, a := range agents {
		if a.Status == "online" {
			online++
		}
	}

	labs, err := s.store.ListLabs(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get labs")
		return
	}

	mappings, err := s.store.ListMappings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get mappings")
		return
	}

	writeJSON(w, http.StatusOK, SummaryResponse{
		TotalAgents:   len(agents),
		OnlineAgents:  online,
		TotalLabs:     len(labs),
		TotalMappings: len(mappings),
	})
}

// safeLabelValue validates and escapes a Prometheus label value to prevent PromQL injection.
// Returns the escaped value and true if safe, or "", false if the value should be rejected.
func safeLabelValue(s string) (string, bool) {
	if s == "" || len(s) > 256 {
		return "", false
	}
	for _, c := range s {
		if c == '\n' || c == '\r' || c == 0 {
			return "", false
		}
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s, true
}

// buildLabelFilters returns a Prometheus label selector with user!="", the
// configured user-ignore matcher, and optional hostname/lab equality matchers.
// The ignore matcher must be applied inside PromQL for app-level reports: they
// aggregate away the user label, so an ignored account cannot be filtered out
// after the fact.
func (s *Server) buildLabelFilters(ctx context.Context, hostname, lab string) string {
	filters := []string{`user!=""`, s.userPolicy(ctx).IgnoreMatcher()}
	if v, ok := safeLabelValue(hostname); ok {
		filters = append(filters, fmt.Sprintf(`hostname="%s"`, v))
	}
	if v, ok := safeLabelValue(lab); ok {
		filters = append(filters, fmt.Sprintf(`lab="%s"`, v))
	}
	return "{" + strings.Join(filters, ",") + "}"
}

// allowedAppSet returns a whitelist of lowercased app names (both display name and exe name)
// that have an active (non-ignored) mapping entry. Only apps in this set appear in reports.
// Returns nil if there are no mappings at all (fresh install), which lets queryAndRespondFiltered
// fall through to show everything rather than showing a blank chart.
func (s *Server) allowedAppSet(ctx context.Context) map[string]bool {
	mappings, err := s.store.ListMappings(ctx)
	if err != nil || len(mappings) == 0 {
		return nil
	}
	set := make(map[string]bool)
	for _, m := range mappings {
		if m.Ignored {
			continue
		}
		if m.DisplayName != "" {
			set[strings.ToLower(m.DisplayName)] = true
		}
		if m.ExeName != "" {
			set[strings.ToLower(m.ExeName)] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// queryAndRespondFiltered queries Prometheus, applies the whitelist, sorts by value,
// and limits to the top/bottom N results. This must be done server-side because topk/
// bottomk in PromQL runs before filtering — unmapped apps can crowd out mapped ones.
// Pass limit=0 to skip server-side limiting. Pass ascending=true for bottomk semantics.
func (s *Server) queryAndRespondFiltered(w http.ResponseWriter, query, format string, atTime int64, allowedApps map[string]bool, limit int, ascending bool) {
	promURL := fmt.Sprintf("%s/api/v1/query?query=%s", s.cfg.Prom.URL, url.QueryEscape(query))
	if atTime > 0 {
		promURL += fmt.Sprintf("&time=%d", atTime)
	}

	resp, err := s.promClient.Get(promURL)
	if err != nil {
		s.logger.Error("prometheus query failed", "error", err, "query", query)
		writeError(w, http.StatusBadGateway, "failed to reach Prometheus")
		return
	}
	defer resp.Body.Close()

	var result promQueryInstantResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		s.logger.Error("failed to decode prometheus response", "error", err)
		writeError(w, http.StatusBadGateway, "failed to parse Prometheus response")
		return
	}

	// Whitelist: keep only apps with an active mapping entry.
	if len(allowedApps) > 0 {
		filtered := result.Data.Result[:0]
		for _, r := range result.Data.Result {
			if allowedApps[strings.ToLower(r.Metric["app"])] {
				filtered = append(filtered, r)
			}
		}
		result.Data.Result = filtered
	}

	// Server-side sort + limit (replaces PromQL topk/bottomk).
	if limit > 0 || ascending {
		parseVal := func(idx int) float64 {
			r := result.Data.Result[idx]
			if len(r.Value) < 2 {
				return 0
			}
			if v, ok := r.Value[1].(string); ok {
				f, _ := strconv.ParseFloat(v, 64)
				return f
			}
			return 0
		}
		sort.SliceStable(result.Data.Result, func(i, j int) bool {
			if ascending {
				return parseVal(i) < parseVal(j)
			}
			return parseVal(i) > parseVal(j)
		})
		if limit > 0 && len(result.Data.Result) > limit {
			result.Data.Result = result.Data.Result[:limit]
		}
	}

	if format == "csv" {
		s.writeCSV(w, result.Data.Result)
	} else {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

// parseCustomTimeRange parses start/end query params (unix timestamp integers or RFC3339).
// Returns the PromQL duration string and end unix timestamp, or "", 0, false if invalid.
func parseCustomTimeRange(startStr, endStr string) (duration string, endUnix int64, ok bool) {
	if startStr == "" || endStr == "" {
		return "", 0, false
	}
	parseT := func(s string) (time.Time, error) {
		if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
			return time.Unix(ts, 0), nil
		}
		return time.Parse(time.RFC3339, s)
	}
	startT, err := parseT(startStr)
	if err != nil {
		return "", 0, false
	}
	endT, err := parseT(endStr)
	if err != nil {
		return "", 0, false
	}
	d := endT.Sub(startT)
	if d <= 0 || d > 366*24*time.Hour {
		return "", 0, false
	}
	return fmt.Sprintf("%ds", int64(d.Seconds())), endT.Unix(), true
}

// validTimeRange returns true if s is a valid Prometheus duration string
// (one or more digits followed by s/m/h/d/w/y). Used to prevent PromQL injection.
func validTimeRange(s string) bool {
	if len(s) < 2 {
		return false
	}
	for i, c := range s {
		if i == len(s)-1 {
			return c == 's' || c == 'm' || c == 'h' || c == 'd' || c == 'w' || c == 'y'
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return false
}

// safeTimeRange returns the given range if valid, otherwise the default.
func safeTimeRange(s, defaultRange string) string {
	if validTimeRange(s) {
		return s
	}
	return defaultRange
}

// proxyPromQuery executes an instant query against Prometheus and returns the result.
func (s *Server) proxyPromQuery(w http.ResponseWriter, query string) {
	promURL := fmt.Sprintf("%s/api/v1/query?query=%s", s.cfg.Prom.URL, url.QueryEscape(query))

	resp, err := s.promClient.Get(promURL)
	if err != nil {
		s.logger.Error("prometheus query failed", "error", err, "query", query)
		writeError(w, http.StatusBadGateway, "failed to reach Prometheus")
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		s.logger.Warn("failed to stream prometheus response to client", "error", err)
	}
}

// ReportTopAppsByLaunches godoc
// @Summary      Top applications by launch count
// @Description  Returns top applications by total launch count over the given time range.
// @Tags         reports
// @Produce      json
// @Param        range  query  string  false  "Time range (e.g. 24h, 7d, 30d)"  default(24h)
// @Param        limit  query  int     false  "Max results"  default(10)
// @Param        format query  string  false  "Output format: json or csv"  default(json)
// @Success      200
// @Router       /api/v1/reports/top-apps-by-launches [get]
func (s *Server) ReportTopAppsByLaunches(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	timeRange := safeTimeRange(q.Get("range"), "24h")
	atTime := int64(0)
	if dur, end, ok := parseCustomTimeRange(q.Get("start"), q.Get("end")); ok {
		timeRange = dur
		atTime = end
	}

	limit := 10
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	lf := s.buildLabelFilters(r.Context(), q.Get("hostname"), q.Get("lab"))
	// topk is applied server-side after whitelist filtering.
	query := fmt.Sprintf(
		`sum by (app, category) (increase(openlabstats_app_launches_total%s[%s])) > 0`,
		lf, timeRange,
	)
	s.queryAndRespondFiltered(w, query, q.Get("format"), atTime, s.allowedAppSet(r.Context()), limit, false)
}

// ReportTopAppsByForegroundTime godoc
// @Summary      Top applications by foreground time
// @Description  Returns top applications by total foreground (active) time.
// @Tags         reports
// @Produce      json
// @Param        range  query  string  false  "Time range"  default(24h)
// @Param        limit  query  int     false  "Max results"  default(10)
// @Param        format query  string  false  "Output format: json or csv"  default(json)
// @Success      200
// @Router       /api/v1/reports/top-apps-by-foreground [get]
func (s *Server) ReportTopAppsByForegroundTime(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	timeRange := safeTimeRange(q.Get("range"), "24h")
	atTime := int64(0)
	if dur, end, ok := parseCustomTimeRange(q.Get("start"), q.Get("end")); ok {
		timeRange = dur
		atTime = end
	}

	limit := 10
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	lf := s.buildLabelFilters(r.Context(), q.Get("hostname"), q.Get("lab"))
	query := fmt.Sprintf(
		`sum by (app, category) (increase(openlabstats_app_foreground_seconds_total%s[%s])) / 3600 > 0`,
		lf, timeRange,
	)
	s.queryAndRespondFiltered(w, query, q.Get("format"), atTime, s.allowedAppSet(r.Context()), limit, false)
}

// ReportBottomAppsByLaunches godoc
// @Summary      Bottom applications by launch count
// @Description  Returns bottom (least used) applications by launch count.
// @Tags         reports
// @Produce      json
// @Param        range  query  string  false  "Time range"  default(24h)
// @Param        limit  query  int     false  "Max results"  default(10)
// @Param        format query  string  false  "Output format: json or csv"  default(json)
// @Success      200
// @Router       /api/v1/reports/bottom-apps-by-launches [get]
func (s *Server) ReportBottomAppsByLaunches(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	timeRange := safeTimeRange(q.Get("range"), "24h")
	atTime := int64(0)
	if dur, end, ok := parseCustomTimeRange(q.Get("start"), q.Get("end")); ok {
		timeRange = dur
		atTime = end
	}

	limit := 10
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	lf := s.buildLabelFilters(r.Context(), q.Get("hostname"), q.Get("lab"))
	query := fmt.Sprintf(
		`sum by (app, category) (increase(openlabstats_app_launches_total%s[%s])) > 0`,
		lf, timeRange,
	)
	s.queryAndRespondFiltered(w, query, q.Get("format"), atTime, s.allowedAppSet(r.Context()), limit, true)
}

// ReportBottomAppsByForegroundTime godoc
// @Summary      Bottom applications by foreground time
// @Description  Returns bottom (least used) applications by foreground time.
// @Tags         reports
// @Produce      json
// @Param        range  query  string  false  "Time range"  default(24h)
// @Param        limit  query  int     false  "Max results"  default(10)
// @Param        format query  string  false  "Output format: json or csv"  default(json)
// @Success      200
// @Router       /api/v1/reports/bottom-apps-by-foreground [get]
func (s *Server) ReportBottomAppsByForegroundTime(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	timeRange := safeTimeRange(q.Get("range"), "24h")
	atTime := int64(0)
	if dur, end, ok := parseCustomTimeRange(q.Get("start"), q.Get("end")); ok {
		timeRange = dur
		atTime = end
	}

	limit := 10
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	lf := s.buildLabelFilters(r.Context(), q.Get("hostname"), q.Get("lab"))
	query := fmt.Sprintf(
		`sum by (app, category) (increase(openlabstats_app_foreground_seconds_total%s[%s])) / 3600 > 0`,
		lf, timeRange,
	)
	s.queryAndRespondFiltered(w, query, q.Get("format"), atTime, s.allowedAppSet(r.Context()), limit, true)
}

// queryAndRespond executes a Prometheus instant query at the current time.
func (s *Server) queryAndRespond(w http.ResponseWriter, query, format string) {
	s.queryAndRespondAt(w, query, format, 0)
}

// queryAndRespondAt executes a Prometheus instant query at the given unix timestamp
// (0 means "now") and returns either JSON or CSV.
func (s *Server) queryAndRespondAt(w http.ResponseWriter, query, format string, atTime int64) {
	promURL := fmt.Sprintf("%s/api/v1/query?query=%s", s.cfg.Prom.URL, url.QueryEscape(query))
	if atTime > 0 {
		promURL += fmt.Sprintf("&time=%d", atTime)
	}

	resp, err := s.promClient.Get(promURL)
	if err != nil {
		s.logger.Error("prometheus query failed", "error", err, "query", query)
		writeError(w, http.StatusBadGateway, "failed to reach Prometheus")
		return
	}
	defer resp.Body.Close()

	var result promQueryInstantResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		s.logger.Error("failed to decode prometheus response", "error", err)
		writeError(w, http.StatusBadGateway, "failed to parse Prometheus response")
		return
	}

	if format == "csv" {
		s.writeCSV(w, result.Data.Result)
	} else {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

// writeCSV writes the Prometheus results as CSV.
// Column names are derived from whichever metric labels are present in the
// results, so reports that group by different label sets (e.g. lab+app vs
// app+category) produce the correct columns without hardcoding them.
func (s *Server) writeCSV(w http.ResponseWriter, results []struct {
	Metric map[string]string `json:"metric"`
	Value  []interface{}     `json:"value"`
}) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=report.csv")

	writer := csv.NewWriter(w)
	defer writer.Flush()

	if len(results) == 0 {
		writer.Write([]string{"value"})
		return
	}

	// Collect label names in a stable order from the first result.
	// Use a preferred ordering for common labels; unknown labels follow.
	preferred := []string{"lab", "building", "room", "app", "category", "hostname", "user"}
	seen := make(map[string]bool)
	var cols []string
	for _, k := range preferred {
		if _, ok := results[0].Metric[k]; ok {
			cols = append(cols, k)
			seen[k] = true
		}
	}
	var extra []string
	for k := range results[0].Metric {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	cols = append(cols, extra...)

	header := append(cols, "value")
	writer.Write(header)

	for _, r := range results {
		var value float64
		if len(r.Value) >= 2 {
			if v, ok := r.Value[1].(string); ok {
				value, _ = strconv.ParseFloat(v, 64)
			}
		}
		row := make([]string, 0, len(cols)+1)
		for _, col := range cols {
			row = append(row, r.Metric[col])
		}
		row = append(row, fmt.Sprintf("%.2f", value))
		writer.Write(row)
	}
}

// buildUserSessionFilters returns a label selector for user session metrics.
// Session metrics only carry user and hostname labels (no lab), so lab is not filtered.
// The ignore matcher also purges system and service accounts that accumulated in
// Prometheus before the agent-side filter covered them.
func (s *Server) buildUserSessionFilters(ctx context.Context, hostname string) string {
	filters := []string{`user!=""`, s.userPolicy(ctx).IgnoreMatcher()}
	if v, ok := safeLabelValue(hostname); ok {
		filters = append(filters, fmt.Sprintf(`hostname="%s"`, v))
	}
	return "{" + strings.Join(filters, ",") + "}"
}

// newVectorResult returns an empty successful instant-vector response. Handlers
// that aggregate in Go rebuild this shape so the frontend can treat their output
// like any other Prometheus query.
func newVectorResult() promQueryInstantResult {
	var result promQueryInstantResult
	result.Status = "success"
	result.Data.ResultType = "vector"
	return result
}

func appendVectorSample(result *promQueryInstantResult, metric map[string]string, value float64) {
	result.Data.Result = append(result.Data.Result, struct {
		Metric map[string]string `json:"metric"`
		Value  []interface{}     `json:"value"`
	}{
		Metric: metric,
		Value:  []interface{}{time.Now().Unix(), fmt.Sprintf("%g", value)},
	})
}

// fetchInstantVector runs an instant query at the given unix timestamp (0 means
// "now") and returns the decoded response.
func (s *Server) fetchInstantVector(ctx context.Context, query string, atTime int64) (promQueryInstantResult, error) {
	var result promQueryInstantResult
	promURL := fmt.Sprintf("%s/api/v1/query?query=%s", s.cfg.Prom.URL, url.QueryEscape(query))
	if atTime > 0 {
		promURL += fmt.Sprintf("&time=%d", atTime)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, promURL, nil)
	if err != nil {
		return result, err
	}
	resp, err := s.promClient.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}

// sumByCanonicalUser runs a `sum by (user)` query and folds the result into
// canonical identities, dropping ignored accounts. This is why user-keyed
// reports cannot use PromQL topk: the merge has to happen before ranking, or a
// user split across Windows and macOS could be ranked twice — or ranked out.
func (s *Server) sumByCanonicalUser(ctx context.Context, query string, atTime int64, policy *userid.Policy) (map[string]float64, error) {
	raw, err := s.fetchInstantVector(ctx, query, atTime)
	if err != nil {
		return nil, err
	}
	totals := make(map[string]float64, len(raw.Data.Result))
	for _, entry := range raw.Data.Result {
		canonical, ignored := policy.Resolve(entry.Metric["user"])
		if ignored || canonical == "" || len(entry.Value) < 2 {
			continue
		}
		v, ok := entry.Value[1].(string)
		if !ok {
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		totals[canonical] += f
	}
	return totals, nil
}

// respondUserTotals sorts canonical-user totals descending, keeps the top
// `limit`, and writes them in Prometheus vector shape (or CSV).
func (s *Server) respondUserTotals(w http.ResponseWriter, totals map[string]float64, format string, limit int) {
	type pair struct {
		user  string
		value float64
	}
	pairs := make([]pair, 0, len(totals))
	for user, value := range totals {
		if value > 0 {
			pairs = append(pairs, pair{user, value})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].value != pairs[j].value {
			return pairs[i].value > pairs[j].value
		}
		return pairs[i].user < pairs[j].user
	})
	if limit > 0 && len(pairs) > limit {
		pairs = pairs[:limit]
	}

	result := newVectorResult()
	for _, p := range pairs {
		appendVectorSample(&result, map[string]string{"user": p.user}, p.value)
	}

	if format == "csv" {
		s.writeCSV(w, result.Data.Result)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// ReportTopDevicesBySessionCount godoc
// @Summary      Top devices by session count
// @Description  Returns the top N hostnames ranked by user login count over the time range.
// @Tags         reports
// @Produce      json
// @Param        range    query  string  false  "Time range"  default(24h)
// @Param        limit    query  int     false  "Max results"  default(10)
// @Param        hostname query  string  false  "Filter to a specific machine"
// @Success      200
// @Router       /api/v1/reports/top-devices-by-sessions [get]
func (s *Server) ReportTopDevicesBySessionCount(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	timeRange := safeTimeRange(q.Get("range"), "24h")
	atTime := int64(0)
	if dur, end, ok := parseCustomTimeRange(q.Get("start"), q.Get("end")); ok {
		timeRange = dur
		atTime = end
	}
	limit := 10
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	lf := s.buildUserSessionFilters(r.Context(), q.Get("hostname"))
	query := fmt.Sprintf(
		`topk(%d, sum by (hostname) (increase(openlabstats_user_session_logins_total%s[%s])) > 0)`,
		limit, lf, timeRange,
	)
	s.queryAndRespondAt(w, query, q.Get("format"), atTime)
}

// ReportTopUsersByLoginCount godoc
// @Summary      Top users by login count
// @Description  Returns the top N users ranked by total login sessions over the time range.
// @Tags         reports
// @Produce      json
// @Param        range    query  string  false  "Time range"  default(24h)
// @Param        limit    query  int     false  "Max results"  default(10)
// @Param        hostname query  string  false  "Filter to a specific machine"
// @Success      200
// @Router       /api/v1/reports/top-users-by-logins [get]
func (s *Server) ReportTopUsersByLoginCount(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	timeRange := safeTimeRange(q.Get("range"), "24h")
	atTime := int64(0)
	if dur, end, ok := parseCustomTimeRange(q.Get("start"), q.Get("end")); ok {
		timeRange = dur
		atTime = end
	}
	limit := 10
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	ctx := r.Context()
	policy := s.userPolicy(ctx)
	lf := s.buildUserSessionFilters(ctx, q.Get("hostname"))
	query := fmt.Sprintf(
		`sum by (user) (increase(openlabstats_user_session_logins_total%s[%s])) > 0`,
		lf, timeRange,
	)
	totals, err := s.sumByCanonicalUser(ctx, query, atTime, policy)
	if err != nil {
		s.logger.Error("prometheus query failed", "error", err, "query", query)
		writeError(w, http.StatusBadGateway, "failed to reach Prometheus")
		return
	}
	s.respondUserTotals(w, totals, q.Get("format"), limit)
}

// ReportTopUsersBySessionTime godoc
// @Summary      Top users by total session time
// @Description  Returns the top N users ranked by total session hours over the time range.
// @Tags         reports
// @Produce      json
// @Param        range    query  string  false  "Time range"  default(24h)
// @Param        limit    query  int     false  "Max results"  default(10)
// @Param        hostname query  string  false  "Filter to a specific machine"
// @Success      200
// @Router       /api/v1/reports/top-users-by-session-time [get]
func (s *Server) ReportTopUsersBySessionTime(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	timeRange := safeTimeRange(q.Get("range"), "24h")
	atTime := int64(0)
	if dur, end, ok := parseCustomTimeRange(q.Get("start"), q.Get("end")); ok {
		timeRange = dur
		atTime = end
	}
	limit := 10
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	ctx := r.Context()
	policy := s.userPolicy(ctx)
	lf := s.buildUserSessionFilters(ctx, q.Get("hostname"))
	query := fmt.Sprintf(
		`sum by (user) (increase(openlabstats_user_session_seconds_total%s[%s])) / 3600 > 0`,
		lf, timeRange,
	)
	totals, err := s.sumByCanonicalUser(ctx, query, atTime, policy)
	if err != nil {
		s.logger.Error("prometheus query failed", "error", err, "query", query)
		writeError(w, http.StatusBadGateway, "failed to reach Prometheus")
		return
	}
	s.respondUserTotals(w, totals, q.Get("format"), limit)
}

// minLoginsForAvgSession is the smallest login count treated as a large
// enough sample to report an average. seconds/logins is total accrued
// signed-in time divided by count of discrete new-login *events* — for a
// shared/kiosk account that's rarely signed all the way out, that count can
// be 1 or 2 while the account stayed signed in for days, producing an
// "average session" of a week-plus that no one actually experienced as one
// sitting (found via genchem: 1 login, 191.87 accrued hours -> an 11,506
// minute "average"). Below this floor a user is omitted from this report
// entirely rather than shown with a misleading number; their total time and
// login count still appear correctly in the other two reports.
const minLoginsForAvgSession = 3

// computeAvgSessionMinutes divides accrued session seconds by login count per
// canonical user, in minutes. A user with an ongoing session but no fresh
// login inside the window has a zero denominator and is omitted rather than
// reported as infinite; a user below minLogins is omitted for being too
// small a sample to average meaningfully (see minLoginsForAvgSession).
func computeAvgSessionMinutes(seconds, logins map[string]float64, minLogins float64) map[string]float64 {
	avgMinutes := make(map[string]float64, len(seconds))
	for user, secs := range seconds {
		if count := logins[user]; count >= minLogins {
			avgMinutes[user] = secs / count / 60
		}
	}
	return avgMinutes
}

// ReportAvgSessionTime godoc
// @Summary      Average session duration per user
// @Description  Returns top N users ranked by average session duration in minutes. Users with fewer than minLoginsForAvgSession logins in the window are omitted — too small a sample to average meaningfully.
// @Tags         reports
// @Produce      json
// @Param        range    query  string  false  "Time range"  default(24h)
// @Param        limit    query  int     false  "Max results"  default(10)
// @Param        hostname query  string  false  "Filter to a specific machine"
// @Success      200
// @Router       /api/v1/reports/avg-session-time [get]
func (s *Server) ReportAvgSessionTime(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	timeRange := safeTimeRange(q.Get("range"), "24h")
	atTime := int64(0)
	if dur, end, ok := parseCustomTimeRange(q.Get("start"), q.Get("end")); ok {
		timeRange = dur
		atTime = end
	}
	limit := 10
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	ctx := r.Context()
	policy := s.userPolicy(ctx)
	lf := s.buildUserSessionFilters(ctx, q.Get("hostname"))
	// Seconds and logins are merged into canonical identities separately and only
	// then divided — averaging the per-raw-username averages would weight a
	// user's Windows and macOS sessions equally regardless of how many of each.
	secondsQuery := fmt.Sprintf(
		`sum by (user) (increase(openlabstats_user_session_seconds_total%s[%s]))`, lf, timeRange)
	loginsQuery := fmt.Sprintf(
		`sum by (user) (increase(openlabstats_user_session_logins_total%s[%s]))`, lf, timeRange)

	seconds, err := s.sumByCanonicalUser(ctx, secondsQuery, atTime, policy)
	if err != nil {
		s.logger.Error("prometheus query failed", "error", err, "query", secondsQuery)
		writeError(w, http.StatusBadGateway, "failed to reach Prometheus")
		return
	}
	logins, err := s.sumByCanonicalUser(ctx, loginsQuery, atTime, policy)
	if err != nil {
		s.logger.Error("prometheus query failed", "error", err, "query", loginsQuery)
		writeError(w, http.StatusBadGateway, "failed to reach Prometheus")
		return
	}

	avgMinutes := computeAvgSessionMinutes(seconds, logins, minLoginsForAvgSession)
	s.respondUserTotals(w, avgMinutes, q.Get("format"), limit)
}

// ReportTopAppsUsage godoc
// @Summary      Top applications by usage time
// @Description  Returns the top applications by total usage seconds over the given time range.
// @Tags         reports
// @Produce      json
// @Param        range  query  string  false  "Time range"  default(24h)
// @Param        limit  query  int     false  "Max results"  default(20)
// @Param        format query  string  false  "Output format: json or csv"  default(json)
// @Success      200
// @Router       /api/v1/reports/top-apps [get]
func (s *Server) ReportTopAppsUsage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	timeRange := safeTimeRange(q.Get("range"), "24h")
	atTime := int64(0)
	if dur, end, ok := parseCustomTimeRange(q.Get("start"), q.Get("end")); ok {
		timeRange = dur
		atTime = end
	}

	limit := 20
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	lf := s.buildLabelFilters(r.Context(), q.Get("hostname"), q.Get("lab"))
	query := fmt.Sprintf(
		`sum by (app, category) (increase(openlabstats_app_usage_seconds_total%s[%s])) / 3600 > 0`,
		lf, timeRange,
	)
	s.queryAndRespondFiltered(w, query, q.Get("format"), atTime, s.allowedAppSet(r.Context()), limit, false)
}

// promQueryRangeResult represents a Prometheus range query (matrix) response.
type promQueryRangeResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]interface{}   `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

type utilizationPoint struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

type utilizationSeries struct {
	Lab   string             `json:"lab"`
	Total int                `json:"total"`
	Data  []utilizationPoint `json:"data"`
}

type utilizationResponse struct {
	Step   int64               `json:"step"`
	Series []utilizationSeries `json:"series"`
}

type labAvgUtilization struct {
	Lab    string  `json:"lab"`
	AvgPct float64 `json:"avgPct"`
	Total  int     `json:"total"`
}

type labTimeKey struct {
	lab string
	t   int64
}

// utilLabTimeData holds the computed per-lab, per-timestamp active machine counts.
type utilLabTimeData struct {
	labTimeSum     map[labTimeKey]float64
	machinesPerLab map[string]int
	timestamps     []int64
	labs           []string
}

// buildUtilizationData runs the Prometheus range query and computes per-(lab, timestamp)
// active machine counts. It is the shared core for both utilization endpoints.
func (s *Server) buildUtilizationData(ctx context.Context, startUnix, endUnix, step int64, labFilter, hnFilter string) (*utilLabTimeData, error) {
	hostnameToLab, err := s.buildHostnameLabMap(ctx)
	if err != nil {
		return nil, err
	}

	stepStr := fmt.Sprintf("%ds", step)
	var hfParts []string
	// Ignored accounts must be excluded here, not after aggregation: a service
	// account's app usage would otherwise mark its machine as occupied.
	hfParts = append(hfParts, `user!=""`, s.userPolicy(ctx).IgnoreMatcher())
	if v, ok := safeLabelValue(hnFilter); ok {
		hfParts = append(hfParts, fmt.Sprintf(`hostname="%s"`, v))
	}
	hf := "{" + strings.Join(hfParts, ",") + "}"
	promQuery := fmt.Sprintf(`rate(openlabstats_app_usage_seconds_total%s[%s])`, hf, stepStr)

	promURL := fmt.Sprintf(
		"%s/api/v1/query_range?query=%s&start=%d&end=%d&step=%d",
		s.cfg.Prom.URL, url.QueryEscape(promQuery), startUnix, endUnix, step,
	)

	resp, err := s.promClient.Get(promURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw promQueryRangeResult
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	allowedApps := s.allowedAppSet(ctx)

	// Pass 1: sum app-usage rates per (hostname, timestamp) for whitelisted apps only.
	type hostTime struct {
		hostname string
		t        int64
	}
	hostTimeRate := make(map[hostTime]float64)

	for _, series := range raw.Data.Result {
		hostname := strings.ToLower(series.Metric["hostname"])
		app := series.Metric["app"]
		if _, inLab := hostnameToLab[hostname]; !inLab {
			continue
		}
		if len(allowedApps) > 0 && !allowedApps[strings.ToLower(app)] {
			continue
		}
		labName := hostnameToLab[hostname]
		if labFilter != "" && !strings.EqualFold(labName, labFilter) {
			continue
		}
		for _, pt := range series.Values {
			if len(pt) < 2 {
				continue
			}
			tsF, ok := pt[0].(float64)
			if !ok {
				continue
			}
			vs, ok := pt[1].(string)
			if !ok {
				continue
			}
			v, _ := strconv.ParseFloat(vs, 64)
			hostTimeRate[hostTime{hostname, int64(tsF)}] += v
		}
	}

	// Denominator: total DB-registered machines per lab.
	machinesPerLab := make(map[string]int)
	for hostname, labName := range hostnameToLab {
		if labFilter != "" && !strings.EqualFold(labName, labFilter) {
			continue
		}
		_ = hostname
		machinesPerLab[labName]++
	}

	// Pass 2: threshold rate > 0 → 1 machine active, then sum by (lab, timestamp).
	labTimeSum := make(map[labTimeKey]float64)
	tsSet := make(map[int64]struct{})

	for ht, rateVal := range hostTimeRate {
		labName := hostnameToLab[ht.hostname]
		var active float64
		if rateVal > 0 {
			active = 1
		}
		labTimeSum[labTimeKey{labName, ht.t}] += active
		tsSet[ht.t] = struct{}{}
	}

	for _, series := range raw.Data.Result {
		for _, pt := range series.Values {
			if tsF, ok := pt[0].(float64); ok {
				tsSet[int64(tsF)] = struct{}{}
			}
		}
	}

	timestamps := make([]int64, 0, len(tsSet))
	for t := range tsSet {
		timestamps = append(timestamps, t)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })

	labs := make([]string, 0, len(machinesPerLab))
	for lab := range machinesPerLab {
		labs = append(labs, lab)
	}
	sort.Strings(labs)

	return &utilLabTimeData{
		labTimeSum:     labTimeSum,
		machinesPerLab: machinesPerLab,
		timestamps:     timestamps,
		labs:           labs,
	}, nil
}

// parseDurationToSecs converts a Prometheus duration string (e.g. "24h", "7d") to seconds.
func parseDurationToSecs(s string) int64 {
	if !validTimeRange(s) || len(s) < 2 {
		return 86400
	}
	val, _ := strconv.ParseInt(s[:len(s)-1], 10, 64)
	switch s[len(s)-1] {
	case 's':
		return val
	case 'm':
		return val * 60
	case 'h':
		return val * 3600
	case 'd':
		return val * 86400
	case 'w':
		return val * 7 * 86400
	case 'y':
		return val * 365 * 86400
	}
	return 86400
}

// parseUtilTimeWindow extracts and validates the time window from query params.
func parseUtilTimeWindow(q url.Values) (startUnix, endUnix, step int64) {
	now := time.Now().Unix()
	endUnix = now
	timeRange := safeTimeRange(q.Get("range"), "24h")
	startUnix = now - parseDurationToSecs(timeRange)

	if _, end, ok := parseCustomTimeRange(q.Get("start"), q.Get("end")); ok {
		endUnix = end
		startUnix = end - parseDurationToSecs(timeRange)
		parseT := func(str string) (int64, bool) {
			if ts, err := strconv.ParseInt(str, 10, 64); err == nil {
				return ts, true
			}
			if t, err := time.Parse(time.RFC3339, str); err == nil {
				return t.Unix(), true
			}
			return 0, false
		}
		if startStr := q.Get("start"); startStr != "" {
			if s, ok := parseT(startStr); ok {
				startUnix = s
			}
		}
		if endStr := q.Get("end"); endStr != "" {
			if e, ok := parseT(endStr); ok {
				endUnix = e
			}
		}
	}

	rangeDur := endUnix - startUnix
	step = rangeDur / 48
	if step < 300 {
		step = 300
	}
	return
}

// ReportUtilizationOverTime godoc
// @Summary      Machine utilization % over time
// @Description  Returns per-lab utilization percentage as a time series.
// @Tags         reports
// @Produce      json
// @Param        range    query  string  false  "Time range (e.g. 24h, 7d, 30d)"  default(24h)
// @Param        hostname query  string  false  "Filter to a specific machine"
// @Param        lab      query  string  false  "Filter to a specific lab"
// @Param        start    query  string  false  "Custom range start (unix or RFC3339)"
// @Param        end      query  string  false  "Custom range end (unix or RFC3339)"
// @Success      200  {object}  utilizationResponse
// @Router       /api/v1/reports/utilization-over-time [get]
func (s *Server) ReportUtilizationOverTime(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	startUnix, endUnix, step := parseUtilTimeWindow(q)

	d, err := s.buildUtilizationData(ctx, startUnix, endUnix, step, q.Get("lab"), q.Get("hostname"))
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to query utilization data")
		return
	}

	var result []utilizationSeries
	for _, lab := range d.labs {
		total := d.machinesPerLab[lab]
		if total == 0 {
			continue
		}
		var data []utilizationPoint
		for _, t := range d.timestamps {
			v := math.Round(d.labTimeSum[labTimeKey{lab, t}]*10) / 10
			data = append(data, utilizationPoint{T: t, V: v})
		}
		if len(data) > 0 {
			result = append(result, utilizationSeries{Lab: lab, Total: total, Data: data})
		}
	}

	writeJSON(w, http.StatusOK, utilizationResponse{Step: step, Series: result})
}

// ReportTopLabsByUtilization godoc
// @Summary      Top labs by average utilization
// @Description  Returns labs ranked by average utilization % over the time range, top 10.
// @Tags         reports
// @Produce      json
// @Param        range  query  string  false  "Time range (e.g. 24h, 7d, 30d)"  default(24h)
// @Param        start  query  string  false  "Custom range start (unix or RFC3339)"
// @Param        end    query  string  false  "Custom range end (unix or RFC3339)"
// @Success      200  {array}  labAvgUtilization
// @Router       /api/v1/reports/top-labs-by-utilization [get]
func (s *Server) ReportTopLabsByUtilization(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	startUnix, endUnix, step := parseUtilTimeWindow(q)

	d, err := s.buildUtilizationData(ctx, startUnix, endUnix, step, "", q.Get("hostname"))
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to query utilization data")
		return
	}

	nTs := len(d.timestamps)
	var result []labAvgUtilization
	for _, lab := range d.labs {
		total := d.machinesPerLab[lab]
		if total == 0 || nTs == 0 {
			continue
		}
		var sumPct float64
		for _, t := range d.timestamps {
			sumPct += (d.labTimeSum[labTimeKey{lab, t}] / float64(total)) * 100
		}
		avg := math.Round(sumPct/float64(nTs)*10) / 10
		result = append(result, labAvgUtilization{Lab: lab, AvgPct: avg, Total: total})
	}

	sort.Slice(result, func(i, j int) bool { return result[i].AvgPct > result[j].AvgPct })
	if len(result) > 10 {
		result = result[:10]
	}

	writeJSON(w, http.StatusOK, result)
}
