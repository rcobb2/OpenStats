package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/rcobb/openlabstats-server/internal/store"
)

// RegisterAgentRequest is the payload agents send when phoning home.
type RegisterAgentRequest struct {
	ID           string `json:"id"`
	Hostname     string `json:"hostname"`
	IPAddress    string `json:"ipAddress"`
	OSVersion    string `json:"osVersion"`
	AgentVersion string `json:"agentVersion"`
	Port         int    `json:"port"`
	Building     string `json:"building"`
	Room         string `json:"room"`
}

type RegisterAgentResponse struct {
	Agent     *store.Agent          `json:"agent"`
	Settings  *store.SystemSettings `json:"settings"`
	UpdateURL string                `json:"updateUrl,omitempty"`
}

// RegisterAgent godoc
// @Summary      Register or heartbeat an agent
// @Description  Agents call this on startup and periodically to register themselves with the server.
// @Tags         agents
// @Accept       json
// @Produce      json
// @Param        body  body  RegisterAgentRequest  true  "Agent registration payload"
// @Success      200   {object}  store.Agent
// @Failure      400   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/v1/agents/register [post]
func (s *Server) RegisterAgent(w http.ResponseWriter, r *http.Request) {
	var req RegisterAgentRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Hostname == "" {
		writeError(w, http.StatusBadRequest, "hostname is required")
		return
	}

	// Default and validate port.
	if req.Port == 0 {
		req.Port = 9183
	}
	if req.Port < 1 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, "port must be between 1 and 65535")
		return
	}

	// Use hostname as ID if not provided.
	if req.ID == "" {
		req.ID = req.Hostname
	}

	// Determine status based on version.
	status := "online"
	settings, err := s.store.GetSettings(r.Context())
	if err != nil {
		s.logger.Warn("failed to get settings for version check", "error", err)
	}
	if settings != nil && settings.MinAgentVersion != "" {
		if isVersionBelow(req.AgentVersion, settings.MinAgentVersion) {
			status = "outdated"
		}
	}

	agent := &store.Agent{
		ID:           req.ID,
		Hostname:     req.Hostname,
		IPAddress:    req.IPAddress,
		OSVersion:    req.OSVersion,
		AgentVersion: req.AgentVersion,
		Port:         req.Port,
		Status:       status,
		Building:     req.Building,
		Room:         req.Room,
	}

	if err := s.store.UpsertAgent(r.Context(), agent); err != nil {
		s.logger.Error("failed to register agent", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to register agent")
		return
	}

	// Refresh Prometheus targets.
	if err := s.discovery.Refresh(r.Context(), s.store); err != nil {
		s.logger.Error("failed to refresh prometheus targets", "error", err)
	}

	s.logger.Info("agent registered", "hostname", req.Hostname, "ip", req.IPAddress)

	// Get the agent to check for pending update (cleared after being returned).
	dbAgent, err := s.store.GetAgent(r.Context(), req.ID)
	if err != nil {
		s.logger.Error("failed to get agent after registration", "error", err)
	}

	// Determine update URL - server-directed takes priority over version-based.
	updateURL := ""

	// First check if server has queued a pending update for this agent.
	if dbAgent != nil && dbAgent.PendingUpdate != "" {
		updateURL = dbAgent.PendingUpdate
		s.logger.Info("sending pending update to agent", "agentID", req.ID, "url", updateURL)
		// Clear the pending update after retrieving it so it won't be sent again.
		_ = s.store.ClearAgentPendingUpdate(r.Context(), req.ID)
	} else if status == "outdated" {
		// Fallback: version-based outdated check.
		updateURL = s.GetLatestInstallerURL(req.OSVersion)
	}

	writeJSON(w, http.StatusOK, RegisterAgentResponse{
		Agent:     agent,
		Settings:  settings,
		UpdateURL: updateURL,
	})
}

// ListAgents godoc
// @Summary      List all enrolled agents
// @Description  Returns all agents that have registered with the server.
// @Tags         agents
// @Produce      json
// @Success      200  {array}  store.Agent
// @Failure      500  {object}  map[string]string
// @Router       /api/v1/agents [get]
func (s *Server) ListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.store.ListAgents(r.Context())
	if err != nil {
		s.logger.Error("failed to list agents", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	}
	if agents == nil {
		agents = []store.Agent{}
	}
	writeJSON(w, http.StatusOK, agents)
}

// GetAgent godoc
// @Summary      Get agent by ID
// @Description  Returns a single agent's details.
// @Tags         agents
// @Produce      json
// @Param        agentID  path  string  true  "Agent ID"
// @Success      200  {object}  store.Agent
// @Failure      404  {object}  map[string]string
// @Router       /api/v1/agents/{agentID} [get]
func (s *Server) GetAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "agentID")
	agent, err := s.store.GetAgent(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent not found")
		} else {
			s.logger.Error("failed to get agent", "id", id, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to get agent")
		}
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

// AssignLabRequest is the payload for assigning an agent to a lab.
type AssignLabRequest struct {
	LabID string `json:"labId"`
}

// AssignAgentToLab godoc
// @Summary      Assign agent to a lab
// @Description  Associates an agent with a lab/room for grouping.
// @Tags         agents
// @Accept       json
// @Produce      json
// @Param        agentID  path  string  true  "Agent ID"
// @Param        body     body  AssignLabRequest  true  "Lab assignment"
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  map[string]string
// @Router       /api/v1/agents/{agentID}/lab [put]
func (s *Server) AssignAgentToLab(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")

	var req AssignLabRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.store.AssignAgentToLab(r.Context(), agentID, req.LabID); err != nil {
		s.logger.Error("failed to assign agent to lab", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to assign agent")
		return
	}

	// Refresh targets so Prometheus picks up new lab labels.
	if err := s.discovery.Refresh(r.Context(), s.store); err != nil {
		s.logger.Error("failed to refresh prometheus targets", "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "assigned"})
}

// DeleteAgent godoc
// @Summary      Remove an agent
// @Description  Removes an agent from the fleet inventory.
// @Tags         agents
// @Param        agentID  path  string  true  "Agent ID"
// @Success      200  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/v1/agents/{agentID} [delete]
func (s *Server) DeleteAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "agentID")
	if err := s.store.DeleteAgent(r.Context(), id); err != nil {
		s.logger.Error("failed to delete agent", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete agent")
		return
	}

	if err := s.discovery.Refresh(r.Context(), s.store); err != nil {
		s.logger.Error("failed to refresh prometheus targets", "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ForceAgentUpdate godoc
// @Summary      Force an agent to update immediately
// @Description  Queues an update for the specified agent which will be delivered on its next heartbeat.
// @Tags         agents
// @Param        agentID  path  string  true  "Agent ID"
// @Success      200  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/v1/agents/{agentID}/force-update [post]
func (s *Server) ForceAgentUpdate(w http.ResponseWriter, r *http.Request) {
	// First validate agent exists.
	agentID := chi.URLParam(r, "agentID")
	dbAgent, err := s.store.GetAgent(r.Context(), agentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent not found")
		} else {
			s.logger.Error("failed to get agent for force-update", "id", agentID, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to get agent")
		}
		return
	}

	url := s.GetLatestInstallerURL(dbAgent.OSVersion)
	if url == "" {
		writeError(w, http.StatusNotFound, "no installer available on server")
		return
	}

	// Queue the update for the agent - it will be delivered on next heartbeat.
	if err := s.store.SetAgentPendingUpdate(r.Context(), agentID, url); err != nil {
		s.logger.Error("failed to queue agent update", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to queue update")
		return
	}

	s.logger.Info("force update queued", "agentID", agentID, "url", url)
	writeJSON(w, http.StatusOK, map[string]string{
		"status":    "queued",
		"message":   "Agent will receive update URL on next heartbeat.",
		"updateUrl": url,
	})
}

// PushAgentMetrics receives a Prometheus text-format snapshot from an agent
// and stores it in the in-memory metrics store so that GET /metrics/agents
// can serve aggregated metrics to Prometheus without requiring inbound access
// to each agent's port 9183.
func (s *Server) PushAgentMetrics(w http.ResponseWriter, r *http.Request) {
	agentID := r.Header.Get("X-Agent-ID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "X-Agent-ID header required")
		return
	}

	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read body")
		return
	}

	// Inject lab/building/room labels so ReportUsageByLab works correctly.
	// In the pull model these came from file_sd target labels; in the push model
	// we must enrich the metrics server-side since agents don't know their lab.
	if labName, building, room, err := s.store.GetAgentLabInfo(r.Context(), agentID); err == nil {
		extra := map[string]string{}
		if labName != "" {
			extra["lab"] = labName
		}
		if building != "" {
			extra["building"] = building
		}
		if room != "" {
			extra["room"] = room
		}
		if len(extra) > 0 {
			body = injectPromLabels(body, extra)
		}
	}

	s.metricsStore.Set(agentID, body)
	w.WriteHeader(http.StatusNoContent)
}

// injectPromLabels adds extra key="value" pairs to every metric line in a
// Prometheus text-format body. Comment and empty lines are left unchanged.
func injectPromLabels(body []byte, extras map[string]string) []byte {
	if len(extras) == 0 {
		return body
	}

	// Build the extra label fragment.
	var sb strings.Builder
	first := true
	for k, v := range extras {
		if !first {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteString(`="`)
		sb.WriteString(promLabelEscape(v))
		sb.WriteByte('"')
		first = false
	}
	extra := sb.String()

	lines := bytes.Split(body, []byte("\n"))
	for i, line := range lines {
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		// Prometheus text format: metric_name{labels} value [timestamp]
		// Label values can contain spaces (e.g. app="Acrobat Update Helper"), so we
		// cannot use the first space as the label-set boundary. Instead, find the
		// last '}' which is the unambiguous end of the label set.
		var newLine []byte
		closeIdx := bytes.LastIndexByte(line, '}')
		if closeIdx >= 0 {
			// Has label set: insert extra labels before the closing }.
			newLine = make([]byte, 0, len(line)+len(extra)+1)
			newLine = append(newLine, line[:closeIdx]...)
			newLine = append(newLine, ',')
			newLine = append(newLine, extra...)
			newLine = append(newLine, '}')
			newLine = append(newLine, line[closeIdx+1:]...)
		} else {
			// No label set: separate metric name from value at the first space,
			// then insert a label set.
			spaceIdx := bytes.IndexByte(line, ' ')
			if spaceIdx <= 0 {
				continue
			}
			newLine = make([]byte, 0, len(line)+len(extra)+2)
			newLine = append(newLine, line[:spaceIdx]...)
			newLine = append(newLine, '{')
			newLine = append(newLine, extra...)
			newLine = append(newLine, '}')
			newLine = append(newLine, line[spaceIdx:]...)
		}
		lines[i] = newLine
	}
	return bytes.Join(lines, []byte("\n"))
}

// promLabelEscape escapes a string for use as a Prometheus label value.
func promLabelEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// ServeAgentMetrics returns all non-stale agent metric snapshots concatenated
// in Prometheus text format. Prometheus scrapes this endpoint instead of each
// agent directly, eliminating the need for inbound access to port 9183.
func (s *Server) ServeAgentMetrics(w http.ResponseWriter, r *http.Request) {
	snapshots := s.metricsStore.GetAll()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	for _, body := range snapshots {
		w.Write(body)
		if len(body) > 0 && body[len(body)-1] != '\n' {
			w.Write([]byte("\n"))
		}
	}
}

func isVersionBelow(current, target string) bool {
	cParts := strings.Split(current, ".")
	tParts := strings.Split(target, ".")

	for i := 0; i < len(cParts) && i < len(tParts); i++ {
		cv, _ := strconv.Atoi(cParts[i])
		tv, _ := strconv.Atoi(tParts[i])
		if cv < tv {
			return true
		}
		if cv > tv {
			return false
		}
	}
	return len(cParts) < len(tParts)
}
