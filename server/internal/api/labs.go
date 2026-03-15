package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rcobb/openlabstats-server/internal/store"
)

// CreateLabRequest is the payload for creating a lab.
type CreateLabRequest struct {
	Name        string `json:"name"`
	Building    string `json:"building"`
	Room        string `json:"room"`
	Description string `json:"description"`
}

// CreateLab godoc
// @Summary      Create a lab/room
// @Description  Creates a new lab or room grouping for agents.
// @Tags         labs
// @Accept       json
// @Produce      json
// @Param        body  body  CreateLabRequest  true  "Lab details"
// @Success      201   {object}  store.Lab
// @Failure      400   {object}  map[string]string
// @Router       /api/v1/labs [post]
func (s *Server) CreateLab(w http.ResponseWriter, r *http.Request) {
	var req CreateLabRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	lab := &store.Lab{
		ID:          generateID(),
		Name:        req.Name,
		Building:    req.Building,
		Room:        req.Room,
		Description: req.Description,
	}

	if err := s.store.CreateLab(r.Context(), lab); err != nil {
		s.logger.Error("failed to create lab", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create lab")
		return
	}

	// In a real app, we'd fetch the row back to get DB-generated timestamps.
	// For now, we manually set them for the response.
	now := time.Now()
	lab.CreatedAt = now
	lab.UpdatedAt = now

	writeJSON(w, http.StatusCreated, lab)
}

// ListLabs godoc
// @Summary      List all labs
// @Description  Returns all configured labs/rooms.
// @Tags         labs
// @Produce      json
// @Success      200  {array}  store.Lab
// @Router       /api/v1/labs [get]
func (s *Server) ListLabs(w http.ResponseWriter, r *http.Request) {
	labs, err := s.store.ListLabs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list labs")
		return
	}
	if labs == nil {
		labs = []store.Lab{}
	}
	writeJSON(w, http.StatusOK, labs)
}

// GetLab godoc
// @Summary      Get lab by ID
// @Tags         labs
// @Produce      json
// @Param        labID  path  string  true  "Lab ID"
// @Success      200  {object}  store.Lab
// @Failure      404  {object}  map[string]string
// @Router       /api/v1/labs/{labID} [get]
func (s *Server) GetLab(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "labID")
	lab, err := s.store.GetLab(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "lab not found")
		return
	}
	writeJSON(w, http.StatusOK, lab)
}

// UpdateLab godoc
// @Summary      Update a lab
// @Tags         labs
// @Accept       json
// @Produce      json
// @Param        labID  path  string  true  "Lab ID"
// @Param        body   body  CreateLabRequest  true  "Updated lab details"
// @Success      200  {object}  map[string]string
// @Router       /api/v1/labs/{labID} [put]
func (s *Server) UpdateLab(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "labID")
	var req CreateLabRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	lab := &store.Lab{
		ID:          id,
		Name:        req.Name,
		Building:    req.Building,
		Room:        req.Room,
		Description: req.Description,
	}
	if err := s.store.UpdateLab(r.Context(), lab); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update lab")
		return
	}

	// Refresh discovery targets in case building/room labels changed.
	if err := s.discovery.Refresh(r.Context(), s.store); err != nil {
		s.logger.Warn("failed to refresh prometheus targets after lab update", "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// DeleteLab godoc
// @Summary      Delete a lab
// @Tags         labs
// @Param        labID  path  string  true  "Lab ID"
// @Success      200  {object}  map[string]string
// @Router       /api/v1/labs/{labID} [delete]
func (s *Server) DeleteLab(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "labID")
	if err := s.store.DeleteLab(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete lab")
		return
	}

	// Refresh discovery targets.
	if err := s.discovery.Refresh(r.Context(), s.store); err != nil {
		s.logger.Warn("failed to refresh prometheus targets after lab deletion", "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
