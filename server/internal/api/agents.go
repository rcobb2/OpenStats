package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

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

	// Default port if not specified.
	if req.Port == 0 {
		req.Port = 9183
	}

	// Use hostname as ID if not provided.
	if req.ID == "" {
		req.ID = req.Hostname
	}

	agent := &store.Agent{
		ID:           req.ID,
		Hostname:     req.Hostname,
		IPAddress:    req.IPAddress,
		OSVersion:    req.OSVersion,
		AgentVersion: req.AgentVersion,
		Port:         req.Port,
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
	writeJSON(w, http.StatusOK, agent)
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
		writeError(w, http.StatusNotFound, "agent not found")
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
