package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	// UpdateChecksum is the lowercase hex SHA-256 of the file UpdateURL points
	// to, computed fresh from disk on every response (installerChecksumForURL
	// caches by mtime/size). Empty when UpdateURL is empty, or when the
	// checksum couldn't be computed — the agent installs without verification
	// in that case rather than refusing the update outright.
	UpdateChecksum  string           `json:"updateChecksum,omitempty"`
	IgnoredExeNames []string         `json:"ignoredExeNames,omitempty"`
	UserPolicy      *AgentUserPolicy `json:"userPolicy,omitempty"`
}

// RegisterAgent godoc
// @Summary      Register or heartbeat an agent
// @Description  Agents call this on startup and periodically to register themselves with the server.
// @Tags         agents
// @Accept       json
// @Produce      json
// @Param        body  body  RegisterAgentRequest  true  "Agent registration payload"
// @Success      200   {object}  RegisterAgentResponse
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

	// Decide what update URL (if any) to hand this agent. Two independent paths:
	//
	//  1. A manual force-update (SetAgentPendingUpdate) always wins and bypasses
	//     both the maintenance window and the rollout throttle — an admin forcing
	//     one machine should not wait its turn. TakeAgentPendingUpdate reads and
	//     clears atomically so concurrent heartbeats can't double-deliver it.
	//  2. Otherwise, when auto-update is enabled, the staggered rollout controller
	//     decides: is the agent below the target version, are we inside the
	//     maintenance window, and is there a free slot in the concurrency budget?
	//
	// See s.decideRolloutUpdate for the throttled path.
	updateURL := ""
	if url, err := s.store.TakeAgentPendingUpdate(r.Context(), req.ID); err != nil {
		s.logger.Error("failed to check pending update", "error", err)
	} else if url != "" {
		updateURL = url
		s.logger.Info("sending pending update to agent", "agentID", req.ID, "url", updateURL)
	} else {
		updateURL = s.decideRolloutUpdate(r.Context(), req, settings)
	}

	var ignoredExeNames []string
	if names, err := s.store.GetIgnoredExeNames(r.Context()); err == nil {
		ignoredExeNames = names
	} else {
		s.logger.Warn("failed to get ignored exe names for heartbeat", "error", err)
	}

	// User policy travels with every heartbeat so ignore rules and identity
	// merges take effect fleet-wide without waiting for an agent restart.
	userPolicy, err := s.buildAgentUserPolicy(r.Context())
	if err != nil {
		s.logger.Warn("failed to build user policy for heartbeat", "error", err)
	}

	writeJSON(w, http.StatusOK, RegisterAgentResponse{
		Agent:           agent,
		Settings:        settings,
		UpdateURL:       updateURL,
		UpdateChecksum:  s.installerChecksumForURL(updateURL),
		IgnoredExeNames: ignoredExeNames,
		UserPolicy:      userPolicy,
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
	s.applyEffectiveStatus(r.Context(), agents)
	writeJSON(w, http.StatusOK, agents)
}

// applyEffectiveStatus overrides each agent's Status with "offline" when it
// hasn't been seen within the configured stale timeout. Status is otherwise
// set once at registration (RegisterAgent) and never updated again, so
// without this, every consumer of this field — the web portal, openstatsctl,
// and the fleet summary's online count — would see an agent that's gone dark
// as "online" (or "outdated") forever. Mutates in place; the DB-stored value
// is left untouched, this only affects what the API returns.
func (s *Server) applyEffectiveStatus(ctx context.Context, agents []store.Agent) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		s.logger.Warn("failed to get settings for stale-agent check", "error", err)
		return
	}
	if settings == nil || settings.StaleTimeoutDays <= 0 {
		return
	}
	staleDur := time.Duration(settings.StaleTimeoutDays) * 24 * time.Hour
	for i := range agents {
		if !agents[i].LastSeen.IsZero() && time.Since(agents[i].LastSeen) > staleDur {
			agents[i].Status = "offline"
		}
	}
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
	single := []store.Agent{*agent}
	s.applyEffectiveStatus(r.Context(), single)
	writeJSON(w, http.StatusOK, single[0])
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
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent not found")
		} else {
			s.logger.Error("failed to assign agent to lab", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to assign agent")
		}
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
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent not found")
		} else {
			s.logger.Error("failed to delete agent", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to delete agent")
		}
		return
	}

	s.metricsStore.Delete(id)

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
		"status":         "queued",
		"message":        "Agent will receive update URL on next heartbeat.",
		"updateUrl":      url,
		"updateChecksum": s.installerChecksumForURL(url),
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

	// Reject pushes from unregistered agents so arbitrary callers cannot inject
	// data into the aggregated metrics endpoint.
	agent, err := s.store.GetAgent(r.Context(), agentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent not registered")
		} else {
			s.logger.Error("failed to verify agent for metrics push", "id", agentID, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to verify agent")
		}
		return
	}

	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read body")
		return
	}

	// Parse into a validated model; a malformed push is rejected and the
	// previous snapshot stays in place so one bad agent cannot break the
	// fleet-wide scrape page.
	fams, err := parseAgentMetrics(body)
	if err != nil {
		s.logger.Warn("rejected agent metrics push", "id", agentID, "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Apply server-side mapping rewrite: rename app/category labels for manually-reviewed
	// entries, drop ignored exe samples, and auto-insert unknown exe names for admin review.
	if mappings, err := s.store.GetMappingsMap(r.Context()); err == nil {
		unknown := applyMappingsToFamilies(fams, mappings)
		if err := s.store.BatchAutoInsertMappings(r.Context(), unknown); err != nil {
			s.logger.Warn("failed to auto-insert mappings", "count", len(unknown), "error", err)
		}
	} else {
		s.logger.Warn("failed to load mappings for label rewrite", "error", err)
	}

	// Lab/building/room labels so ReportUsageByLab works correctly. In the push
	// model the server enriches metrics since agents don't know their lab.
	extra := map[string]string{}
	if labName, building, room, err := s.store.GetAgentLabInfo(r.Context(), agentID); err == nil {
		if labName != "" {
			extra["lab"] = labName
		}
		if building != "" {
			extra["building"] = building
		}
		if room != "" {
			extra["room"] = room
		}
	}
	if n := stampServerLabels(fams, agent.Hostname, extra); n > 0 {
		s.logger.Debug("dropped agent series with a foreign hostname or duplicate labels", "id", agentID, "dropped", n)
	}

	s.metricsStore.Set(agentID, fams)
	w.WriteHeader(http.StatusNoContent)
}

// builtInIgnoreExeSuffixes matches ephemeral, per-version temp executables
// that should never become a mapping row at all — not even an auto-
// discovered, admin-ignored one. Chrome's updater drops one of these per
// version per machine (confirmed live: 32 distinct version-stamped rows,
// e.g. "151.0.7922.77_chrome_installer_uncompressed.exe", all already
// manually ignored one at a time); without this, the exact same pattern
// just keeps accumulating a new row on every future Chrome update,
// forever, for a file that is never a real "application" in any
// software-metering sense.
var builtInIgnoreExeSuffixes = []string{
	"_chrome_installer.exe",
	"_chrome_installer_uncompressed.exe",
}

func isBuiltInIgnoredExe(exeNameLower string) bool {
	for _, suffix := range builtInIgnoreExeSuffixes {
		if strings.HasSuffix(exeNameLower, suffix) {
			return true
		}
	}
	return false
}

// ServeAgentMetrics returns all non-stale agent metric snapshots merged (one
// HELP/TYPE per family) in Prometheus text format. Prometheus scrapes this endpoint instead of each
// agent directly, eliminating the need for inbound access to port 9183.
func (s *Server) ServeAgentMetrics(w http.ResponseWriter, r *http.Request) {
	merged := mergeAgentFamilies(s.metricsStore.GetAll(), s.logger)
	var buf bytes.Buffer
	if err := encodeFamilies(&buf, merged); err != nil {
		s.logger.Error("failed to encode agent metrics", "error", err)
		http.Error(w, "failed to encode metrics", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Write(buf.Bytes())
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
