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
	hf := buildLabelFilters(q.Get("hostname"), "")
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
	query := `openlabstats_user_session_active{user!=""} == 1`
	s.proxyPromQuery(w, query)
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

// buildLabelFilters returns a Prometheus label selector with user!="" and optional
// hostname/lab equality matchers.
func buildLabelFilters(hostname, lab string) string {
	filters := []string{`user!=""`}
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
	limit := 10
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	lf := buildLabelFilters(q.Get("hostname"), q.Get("lab"))
	// Use cumulative counters — apps that started before the time window still show launches.
	// topk is applied server-side after whitelist filtering.
	query := fmt.Sprintf(
		`sum by (app, category) (openlabstats_app_launches_total%s) > 0`,
		lf,
	)
	s.queryAndRespondFiltered(w, query, q.Get("format"), 0, s.allowedAppSet(r.Context()), limit, false)
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

	lf := buildLabelFilters(q.Get("hostname"), q.Get("lab"))
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
	limit := 10
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	lf := buildLabelFilters(q.Get("hostname"), q.Get("lab"))
	query := fmt.Sprintf(
		`sum by (app, category) (openlabstats_app_launches_total%s) > 0`,
		lf,
	)
	s.queryAndRespondFiltered(w, query, q.Get("format"), 0, s.allowedAppSet(r.Context()), limit, true)
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

	lf := buildLabelFilters(q.Get("hostname"), q.Get("lab"))
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
// The regex exclusion purges system accounts that accumulated in Prometheus before the
// agent-side isValidUser guard was deployed.
func buildUserSessionFilters(hostname string) string {
	const sysExclude = `Font Driver Host.*|Window Manager.*|.*UMFD.*|panopto_upload|System|Local Service|Network Service|TrustedInstaller`
	filters := []string{`user!=""`, fmt.Sprintf(`user!~"%s"`, sysExclude)}
	if v, ok := safeLabelValue(hostname); ok {
		filters = append(filters, fmt.Sprintf(`hostname="%s"`, v))
	}
	return "{" + strings.Join(filters, ",") + "}"
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
	limit := 10
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	lf := buildUserSessionFilters(q.Get("hostname"))
	// Login events are sparse — use cumulative counters so any historical data shows.
	query := fmt.Sprintf(
		`topk(%d, sum by (hostname) (openlabstats_user_session_logins_total%s) > 0)`,
		limit, lf,
	)
	s.queryAndRespond(w, query, q.Get("format"))
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
	limit := 10
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	lf := buildUserSessionFilters(q.Get("hostname"))
	// Login events are sparse — use cumulative counters so any historical data shows.
	query := fmt.Sprintf(
		`topk(%d, sum by (user) (openlabstats_user_session_logins_total%s) > 0)`,
		limit, lf,
	)
	s.queryAndRespond(w, query, q.Get("format"))
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
	lf := buildUserSessionFilters(q.Get("hostname"))
	query := fmt.Sprintf(
		`topk(%d, sum by (user) (increase(openlabstats_user_session_seconds_total%s[%s])) / 3600 > 0)`,
		limit, lf, timeRange,
	)
	s.queryAndRespondAt(w, query, q.Get("format"), atTime)
}

// ReportAvgSessionTime godoc
// @Summary      Average session duration per user
// @Description  Returns top N users ranked by average session duration in minutes.
// @Tags         reports
// @Produce      json
// @Param        range    query  string  false  "Time range"  default(24h)
// @Param        limit    query  int     false  "Max results"  default(10)
// @Param        hostname query  string  false  "Filter to a specific machine"
// @Success      200
// @Router       /api/v1/reports/avg-session-time [get]
func (s *Server) ReportAvgSessionTime(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 10
	if l := q.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	lf := buildUserSessionFilters(q.Get("hostname"))
	// Divide cumulative session seconds by cumulative login count to get average session minutes.
	query := fmt.Sprintf(
		`topk(%d, (sum by (user) (openlabstats_user_session_seconds_total%s) / sum by (user) (openlabstats_user_session_logins_total%s)) / 60 > 0)`,
		limit, lf, lf,
	)
	s.queryAndRespond(w, query, q.Get("format"))
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

	lf := buildLabelFilters(q.Get("hostname"), q.Get("lab"))
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

// ReportUtilizationOverTime godoc
// @Summary      Machine utilization % over time
// @Description  Returns per-lab utilization percentage as a time series. Utilization is
//               the average fraction of machines with an active user session, as a percentage.
//               Lab assignment uses current DB state, so reassigned machines are reflected immediately.
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

	// Determine time window.
	now := time.Now().Unix()
	endUnix := now
	timeRange := safeTimeRange(q.Get("range"), "24h")
	startUnix := now - parseDurationToSecs(timeRange)

	if dur, end, ok := parseCustomTimeRange(q.Get("start"), q.Get("end")); ok {
		_ = dur
		endUnix = end
		startUnix = end - parseDurationToSecs(timeRange)
		// If both start and end are given as absolute timestamps, honour them directly.
		if startStr, endStr := q.Get("start"), q.Get("end"); startStr != "" && endStr != "" {
			parseT := func(str string) (int64, bool) {
				if ts, err := strconv.ParseInt(str, 10, 64); err == nil {
					return ts, true
				}
				if t, err := time.Parse(time.RFC3339, str); err == nil {
					return t.Unix(), true
				}
				return 0, false
			}
			if s, ok := parseT(startStr); ok {
				startUnix = s
			}
			if e, ok := parseT(endStr); ok {
				endUnix = e
			}
		}
	}

	// Auto-step: ~48 data points, minimum 5 min.
	rangeDur := endUnix - startUnix
	step := rangeDur / 48
	if step < 300 {
		step = 300
	}

	hostnameToLab, err := s.buildHostnameLabMap(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build lab map")
		return
	}
	labFilter := q.Get("lab")

	// Query raw per-(hostname, app, user) series so we can apply the app whitelist
	// and system-account exclusion server-side before aggregating.
	// We intentionally do NOT sum by hostname in PromQL — that would prevent filtering.
	const sysUserExclude = `Font Driver Host.*|Window Manager.*|.*UMFD.*|panopto_upload|System|Local Service|Network Service|TrustedInstaller`
	stepStr := fmt.Sprintf("%ds", step)
	hn := q.Get("hostname")
	var hfParts []string
	hfParts = append(hfParts, `user!=""`, fmt.Sprintf(`user!~"%s"`, sysUserExclude))
	if v, ok := safeLabelValue(hn); ok {
		hfParts = append(hfParts, fmt.Sprintf(`hostname="%s"`, v))
	}
	hf := "{" + strings.Join(hfParts, ",") + "}"
	promQuery := fmt.Sprintf(
		`rate(openlabstats_app_usage_seconds_total%s[%s])`,
		hf, stepStr,
	)

	promURL := fmt.Sprintf(
		"%s/api/v1/query_range?query=%s&start=%d&end=%d&step=%d",
		s.cfg.Prom.URL, url.QueryEscape(promQuery), startUnix, endUnix, step,
	)

	resp, err := s.promClient.Get(promURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to reach Prometheus")
		return
	}
	defer resp.Body.Close()

	var raw promQueryRangeResult
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadGateway, "failed to parse Prometheus response")
		return
	}

	allowedApps := s.allowedAppSet(ctx)

	// Pass 1: for each (hostname, timestamp), sum app-usage rates for whitelisted apps only.
	type hostTime struct {
		hostname string
		t        int64
	}
	hostTimeRate := make(map[hostTime]float64)

	for _, series := range raw.Data.Result {
		hostname := strings.ToLower(series.Metric["hostname"])
		app := series.Metric["app"]
		if _, inLab := hostnameToLab[hostname]; !inLab {
			continue // machine not registered in DB
		}
		if len(allowedApps) > 0 && !allowedApps[strings.ToLower(app)] {
			continue // unmapped or ignored app
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

	// Denominator: total DB-registered machines per lab (idle machines count as 0%).
	machinesPerLab := make(map[string]int)
	for hostname, labName := range hostnameToLab {
		if labFilter != "" && !strings.EqualFold(labName, labFilter) {
			continue
		}
		_ = hostname
		machinesPerLab[labName]++
	}

	// Pass 2: clamp each (hostname, timestamp) rate to [0,1], then sum by (lab, timestamp).
	type labTime struct {
		lab string
		t   int64
	}
	labTimeSum := make(map[labTime]float64)
	tsSet := make(map[int64]struct{})

	for ht, rateVal := range hostTimeRate {
		labName := hostnameToLab[ht.hostname]
		clamped := rateVal
		if clamped > 1 {
			clamped = 1
		}
		k := labTime{labName, ht.t}
		labTimeSum[k] += clamped
		tsSet[ht.t] = struct{}{}
	}

	// Also populate tsSet from all series timestamps so we can emit 0% points.
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

	var result []utilizationSeries
	for _, lab := range labs {
		total := machinesPerLab[lab]
		if total == 0 {
			continue
		}
		var data []utilizationPoint
		for _, t := range timestamps {
			k := labTime{lab, t}
			// Send raw machine count (sum of per-machine [0,1] values); frontend
			// computes % using Total so it can toggle between the two views.
			raw := math.Round(labTimeSum[k]*10) / 10
			data = append(data, utilizationPoint{T: t, V: raw})
		}
		if len(data) > 0 {
			result = append(result, utilizationSeries{Lab: lab, Total: total, Data: data})
		}
	}

	writeJSON(w, http.StatusOK, utilizationResponse{Step: step, Series: result})
}
