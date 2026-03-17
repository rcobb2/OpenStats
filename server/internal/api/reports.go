package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// promQueryResult represents the structure of a Prometheus query_range response.
type promQueryResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string            `json:"resultType"`
		Result     []json.RawMessage `json:"result"`
	} `json:"data"`
}

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

// ReportTopApps godoc
// @Summary      Top applications by usage
// @Description  Returns the top applications by total usage hours over the given time range. Defaults to last 24 hours.
// @Tags         reports
// @Produce      json
// @Param        range  query  string  false  "Time range for the query (e.g. 24h, 7d, 30d)"  default(24h)
// @Param        limit  query  int     false  "Max number of results"  default(20)
// @Success      200  {object}  map[string]interface{}
// @Failure      502  {object}  map[string]string
// @Router       /api/v1/reports/top-apps [get]
func (s *Server) ReportTopApps(w http.ResponseWriter, r *http.Request) {
	timeRange := r.URL.Query().Get("range")
	if timeRange == "" {
		timeRange = "24h"
	}

	query := fmt.Sprintf(
		`topk(20, sum by (app, category) (increase(openlabstats_app_usage_seconds_total[%s])))`,
		timeRange,
	)
	s.proxyPromQuery(w, query)
}

// ReportUsageByLab godoc
// @Summary      Usage breakdown by lab
// @Description  Returns total app usage hours grouped by lab over the given time range.
// @Tags         reports
// @Produce      json
// @Param        range  query  string  false  "Time range"  default(24h)
// @Success      200  {object}  map[string]interface{}
// @Failure      502  {object}  map[string]string
// @Router       /api/v1/reports/usage-by-lab [get]
func (s *Server) ReportUsageByLab(w http.ResponseWriter, r *http.Request) {
	timeRange := r.URL.Query().Get("range")
	if timeRange == "" {
		timeRange = "24h"
	}

	query := fmt.Sprintf(
		`sum by (lab, app) (increase(openlabstats_app_usage_seconds_total[%s]))`,
		timeRange,
	)
	s.proxyPromQuery(w, query)
}

// ReportActiveUsers godoc
// @Summary      Currently active users
// @Description  Returns users with active sessions right now.
// @Tags         reports
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Router       /api/v1/reports/active-users [get]
func (s *Server) ReportActiveUsers(w http.ResponseWriter, r *http.Request) {
	query := `openlabstats_user_session_active == 1`
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

// proxyPromQuery executes an instant query against Prometheus and returns the result.
func (s *Server) proxyPromQuery(w http.ResponseWriter, query string) {
	promURL := fmt.Sprintf("%s/api/v1/query?query=%s", s.cfg.Prom.URL, url.QueryEscape(query))

	resp, err := http.Get(promURL)
	if err != nil {
		s.logger.Error("prometheus query failed", "error", err, "query", query)
		writeError(w, http.StatusBadGateway, "failed to reach Prometheus")
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
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
	timeRange := r.URL.Query().Get("range")
	if timeRange == "" {
		timeRange = "24h"
	}

	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	query := fmt.Sprintf(
		`topk(%d, sum by (app, category) (increase(openlabstats_app_launches_total[%s])))`,
		limit, timeRange,
	)
	s.queryAndRespond(w, query, r.URL.Query().Get("format"))
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
	timeRange := r.URL.Query().Get("range")
	if timeRange == "" {
		timeRange = "24h"
	}

	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	query := fmt.Sprintf(
		`topk(%d, sum by (app, category) (increase(openlabstats_app_foreground_seconds_total[%s])) / 3600)`,
		limit, timeRange,
	)
	s.queryAndRespond(w, query, r.URL.Query().Get("format"))
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
	timeRange := r.URL.Query().Get("range")
	if timeRange == "" {
		timeRange = "24h"
	}

	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	query := fmt.Sprintf(
		`bottomk(%d, sum by (app, category) (increase(openlabstats_app_launches_total[%s])))`,
		limit, timeRange,
	)
	s.queryAndRespond(w, query, r.URL.Query().Get("format"))
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
	timeRange := r.URL.Query().Get("range")
	if timeRange == "" {
		timeRange = "24h"
	}

	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	query := fmt.Sprintf(
		`bottomk(%d, sum by (app, category) (increase(openlabstats_app_foreground_seconds_total[%s])) / 3600)`,
		limit, timeRange,
	)
	s.queryAndRespond(w, query, r.URL.Query().Get("format"))
}

// queryAndRespond executes a Prometheus query and returns either JSON or CSV.
func (s *Server) queryAndRespond(w http.ResponseWriter, query, format string) {
	promURL := fmt.Sprintf("%s/api/v1/query?query=%s", s.cfg.Prom.URL, url.QueryEscape(query))

	resp, err := http.Get(promURL)
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
func (s *Server) writeCSV(w http.ResponseWriter, results []struct {
	Metric map[string]string `json:"metric"`
	Value  []interface{}     `json:"value"`
}) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=report.csv")

	writer := csv.NewWriter(w)
	defer writer.Flush()

	// Write header
	writer.Write([]string{"application", "category", "value"})

	// Write data rows
	for _, r := range results {
		app := r.Metric["app"]
		category := r.Metric["category"]

		var value float64
		if len(r.Value) >= 2 {
			if v, ok := r.Value[1].(string); ok {
				value, _ = strconv.ParseFloat(v, 64)
			}
		}

		writer.Write([]string{app, category, fmt.Sprintf("%.0f", value)})
	}
}

// ReportTopAppsUsage godoc
// @Summary      Top applications by usage time (legacy)
// @Description  Returns the top applications by total usage hours. Use top-apps-by-launches or top-apps-by-foreground instead.
// @Tags         reports
// @Produce      json
// @Param        range  query  string  false  "Time range"  default(24h)
// @Param        limit  query  int     false  "Max results"  default(20)
// @Success      200
// @Router       /api/v1/reports/top-apps [get]
func (s *Server) ReportTopAppsUsage(w http.ResponseWriter, r *http.Request) {
	timeRange := r.URL.Query().Get("range")
	if timeRange == "" {
		timeRange = "24h"
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	query := fmt.Sprintf(
		`topk(%d, sum by (app, category) (increase(openlabstats_app_usage_seconds_total[%s])))`,
		limit, timeRange,
	)
	s.proxyPromQuery(w, query)
}
