package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
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
	q := r.URL.Query()
	timeRange := safeTimeRange(q.Get("range"), "24h")
	atTime := int64(0)
	if dur, end, ok := parseCustomTimeRange(q.Get("start"), q.Get("end")); ok {
		timeRange = dur
		atTime = end
	}

	lf := buildLabelFilters(q.Get("hostname"), q.Get("lab"))
	query := fmt.Sprintf(
		`sum by (lab, app) (increase(openlabstats_app_usage_seconds_total%s[%s])) > 0`,
		lf, timeRange,
	)
	s.queryAndRespondAt(w, query, q.Get("format"), atTime)
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

	lf := buildLabelFilters(q.Get("hostname"), q.Get("lab"))
	query := fmt.Sprintf(
		`topk(%d, sum by (app, category) (increase(openlabstats_app_launches_total%s[%s])) > 0)`,
		limit, lf, timeRange,
	)
	s.queryAndRespondAt(w, query, q.Get("format"), atTime)
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
		`topk(%d, sum by (app, category) (increase(openlabstats_app_foreground_seconds_total%s[%s])) / 3600 > 0)`,
		limit, lf, timeRange,
	)
	s.queryAndRespondAt(w, query, q.Get("format"), atTime)
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

	lf := buildLabelFilters(q.Get("hostname"), q.Get("lab"))
	query := fmt.Sprintf(
		`bottomk(%d, sum by (app, category) (increase(openlabstats_app_launches_total%s[%s])) > 0)`,
		limit, lf, timeRange,
	)
	s.queryAndRespondAt(w, query, q.Get("format"), atTime)
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
		`bottomk(%d, sum by (app, category) (increase(openlabstats_app_foreground_seconds_total%s[%s])) / 3600 > 0)`,
		limit, lf, timeRange,
	)
	s.queryAndRespondAt(w, query, q.Get("format"), atTime)
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
func buildUserSessionFilters(hostname string) string {
	filters := []string{`user!=""`}
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
	lf := buildUserSessionFilters(q.Get("hostname"))
	query := fmt.Sprintf(
		`topk(%d, sum by (user) (increase(openlabstats_user_session_logins_total%s[%s])) > 0)`,
		limit, lf, timeRange,
	)
	s.queryAndRespondAt(w, query, q.Get("format"), atTime)
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
		`topk(%d, (sum by (user) (increase(openlabstats_user_session_seconds_total%s[%s])) / sum by (user) (increase(openlabstats_user_session_logins_total%s[%s]))) / 60 > 0)`,
		limit, lf, timeRange, lf, timeRange,
	)
	s.queryAndRespondAt(w, query, q.Get("format"), atTime)
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
		`topk(%d, sum by (app, category) (increase(openlabstats_app_usage_seconds_total%s[%s])) / 3600 > 0)`,
		limit, lf, timeRange,
	)
	s.queryAndRespondAt(w, query, q.Get("format"), atTime)
}
