package api

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/rcobb/openlabstats-server/internal/store"
)

// MappingRequest is the payload for creating or updating a mapping.
type MappingRequest struct {
	ExeName     string `json:"exeName"`
	DisplayName string `json:"displayName"`
	Category    string `json:"category"`
	Publisher   string `json:"publisher"`
	Family      string `json:"family"`
	Ignored     bool   `json:"ignored"`
}

// validCategories is the closed set of categories a human can assign through
// the portal or CLI. It exists because free-text entry produced "Productive"
// and "business" sitting alongside "Productivity" and "Business" — typos that
// silently split an app's usage across two buckets in any category rollup.
// It intentionally does NOT constrain what an agent reports on a metric line
// (agent/configs/software-map.json uses a different, richer taxonomy) — those
// land as source="auto" hints that never rewrite metrics until a human edits
// them through this validated path.
var validCategories = map[string]bool{
	"":              true, // uncategorized is allowed; typos are not
	"AI":            true,
	"Browser":       true,
	"Business":      true,
	"Communication": true,
	"Creative":      true,
	"Management":    true,
	"Music":         true,
	"Productivity":  true,
	"Programming":   true,
	"Scientific":    true,
	"Utility":       true,
	"Unknown":       true,
}

func validCategoryNames() []string {
	names := make([]string, 0, len(validCategories))
	for c := range validCategories {
		if c != "" {
			names = append(names, c)
		}
	}
	sort.Strings(names)
	return names
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

// ListMappingCategories godoc
// @Summary      List valid mapping categories
// @Description  Returns the closed set of categories the portal/CLI accept when creating or editing a mapping.
// @Tags         mappings
// @Produce      json
// @Success      200  {array}  string
// @Router       /api/v1/mappings/categories [get]
func (s *Server) ListMappingCategories(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, validCategoryNames())
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
	if !validCategories[req.Category] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid category %q; must be one of: %s",
			req.Category, strings.Join(validCategoryNames(), ", ")))
		return
	}

	m := &store.SoftwareMapping{
		ExeName:     req.ExeName,
		DisplayName: req.DisplayName,
		Category:    req.Category,
		Publisher:   req.Publisher,
		Family:      req.Family,
		Source:      "manual",
		Ignored:     req.Ignored,
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
	if req.ExeName == "" || req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "exeName and displayName are required")
		return
	}
	if !validCategories[req.Category] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid category %q; must be one of: %s",
			req.Category, strings.Join(validCategoryNames(), ", ")))
		return
	}

	m := &store.SoftwareMapping{
		ExeName:     req.ExeName,
		DisplayName: req.DisplayName,
		Category:    req.Category,
		Publisher:   req.Publisher,
		Family:      req.Family,
		Source:      "manual",
		Ignored:     req.Ignored,
	}
	if err := s.store.UpsertMapping(r.Context(), m); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update mapping")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// ToggleMappingIgnored godoc
// @Summary      Toggle mapping ignored state
// @Description  Sets or clears the ignored flag for a mapping without changing other fields.
// @Tags         mappings
// @Accept       json
// @Produce      json
// @Param        mappingID  path  int     true  "Mapping ID"
// @Param        body       body  object  true  "Ignored state {ignored: bool}"
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /api/v1/mappings/{mappingID}/ignore [patch]
func (s *Server) ToggleMappingIgnored(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "mappingID")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mapping ID")
		return
	}

	var req struct {
		Ignored bool `json:"ignored"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.store.SetMappingIgnored(r.Context(), id, req.Ignored); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "mapping not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to update mapping")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// QuickIgnoreApp finds or creates a mapping by display name and marks it ignored.
// Called from the reports page "Ignore" button on chart bars for unmapped apps.
func (s *Server) QuickIgnoreApp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	name := strings.TrimSpace(req.Name)
	ctx := r.Context()
	lowerName := strings.ToLower(name)

	mappings, err := s.store.ListMappings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list mappings")
		return
	}

	// Find existing mapping by display name or exe name (case-insensitive).
	for _, m := range mappings {
		if strings.ToLower(m.DisplayName) == lowerName || strings.ToLower(m.ExeName) == lowerName {
			if err := s.store.SetMappingIgnored(ctx, m.ID, true); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to update mapping")
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
			return
		}
	}

	// No existing mapping — create one marked ignored.
	if err := s.store.UpsertMapping(ctx, &store.SoftwareMapping{
		ExeName:     name,
		DisplayName: name,
		Ignored:     true,
		Source:      "user",
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create mapping")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "created"})
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
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "mapping not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to delete mapping")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
