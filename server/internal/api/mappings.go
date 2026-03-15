package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/rcobb/openlabstats-server/internal/store"
)

// MappingRequest is the payload for creating or updating a mapping.
type MappingRequest struct {
	ExeName     string `json:"exeName"`
	DisplayName string `json:"displayName"`
	Category    string `json:"category"`
	Publisher   string `json:"publisher"`
	Family      string `json:"family"`
}

// GetAgentMappings godoc
// @Summary      Get mappings for agents
// @Description  Returns all software mappings in the format agents expect (software-map.json compatible). Agents poll this endpoint via mappingUpdateURL.
// @Tags         mappings
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Router       /api/v1/mappings/agent [get]
func (s *Server) GetAgentMappings(w http.ResponseWriter, r *http.Request) {
	result, err := s.store.GetMappingsAsAgentJSON(r.Context())
	if err != nil {
		s.logger.Error("failed to get agent mappings", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get mappings")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ListMappings godoc
// @Summary      List all software mappings
// @Description  Returns all software name mappings for admin management.
// @Tags         mappings
// @Produce      json
// @Success      200  {array}  store.SoftwareMapping
// @Router       /api/v1/mappings [get]
func (s *Server) ListMappings(w http.ResponseWriter, r *http.Request) {
	mappings, err := s.store.ListMappings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list mappings")
		return
	}
	if mappings == nil {
		mappings = []store.SoftwareMapping{}
	}
	writeJSON(w, http.StatusOK, mappings)
}

// CreateMapping godoc
// @Summary      Create a software mapping
// @Description  Adds a new exe-to-display-name mapping.
// @Tags         mappings
// @Accept       json
// @Produce      json
// @Param        body  body  MappingRequest  true  "Mapping details"
// @Success      201   {object}  map[string]string
// @Failure      400   {object}  map[string]string
// @Router       /api/v1/mappings [post]
func (s *Server) CreateMapping(w http.ResponseWriter, r *http.Request) {
	var req MappingRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ExeName == "" || req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "exeName and displayName are required")
		return
	}

	m := &store.SoftwareMapping{
		ExeName:     req.ExeName,
		DisplayName: req.DisplayName,
		Category:    req.Category,
		Publisher:   req.Publisher,
		Family:      req.Family,
		Source:      "manual",
	}
	if err := s.store.UpsertMapping(r.Context(), m); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create mapping")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

// UpdateMapping godoc
// @Summary      Update a software mapping
// @Description  Updates an existing exe-to-display-name mapping (upsert by exeName).
// @Tags         mappings
// @Accept       json
// @Produce      json
// @Param        body  body  MappingRequest  true  "Mapping details"
// @Success      200   {object}  map[string]string
// @Router       /api/v1/mappings [put]
func (s *Server) UpdateMapping(w http.ResponseWriter, r *http.Request) {
	var req MappingRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	m := &store.SoftwareMapping{
		ExeName:     req.ExeName,
		DisplayName: req.DisplayName,
		Category:    req.Category,
		Publisher:   req.Publisher,
		Family:      req.Family,
		Source:      "manual",
	}
	if err := s.store.UpsertMapping(r.Context(), m); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update mapping")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// DeleteMapping godoc
// @Summary      Delete a software mapping
// @Tags         mappings
// @Param        mappingID  path  int  true  "Mapping ID"
// @Success      200  {object}  map[string]string
// @Router       /api/v1/mappings/{mappingID} [delete]
func (s *Server) DeleteMapping(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "mappingID")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mapping ID")
		return
	}
	if err := s.store.DeleteMapping(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete mapping")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
