package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// promQueryResult represents the structure of a Prometheus query_range response.
type promQueryResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string            `json:"resultType"`
		Result     []json.RawMessage `json:"result"`
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
	TotalAgents  int `json:"totalAgents"`
	OnlineAgents int `json:"onlineAgents"`
	TotalLabs    int `json:"totalLabs"`
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
		TotalAgents:  len(agents),
		OnlineAgents: online,
		TotalLabs:    len(labs),
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
