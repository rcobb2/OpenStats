package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/rcobb/openlabstats-server/internal/store"
)

// CreateLabRequest is the payload for creating a lab.
type CreateLabRequest struct {
	Name        string `json:"name"`
	Building    string `json:"building"`
	Room        string `json:"room"`
	Description string `json:"description"`
}

// checkLabCollision writes a 409 and returns a non-nil error if building and
// room both match an existing lab's exactly (a blank building or room never
// collides — UpsertAgent's auto-create path only fires when both are set).
// excludeID skips a lab against its own unchanged values during an update.
//
// UpsertAgent's lab lookup (postgres.go: SELECT id FROM labs WHERE
// building = $1 AND room = $2) uses QueryRow, which returns an arbitrary one
// of multiple matching rows — a second lab sharing an existing lab's
// building+room, created or edited through this handler, makes that lookup
// nondeterministic. Same fragmentation-bug family as UpsertAgent's own fix
// earlier this session, just reachable via this manual path instead of the
// heartbeat auto-create path.
func (s *Server) checkLabCollision(ctx context.Context, w http.ResponseWriter, building, room, excludeID string) error {
	if building == "" || room == "" {
		return nil
	}
	labs, err := s.store.ListLabs(ctx)
	if err != nil {
		return nil // best-effort check; don't block the write on a lookup failure
	}
	if l := findLabCollision(labs, building, room, excludeID); l != nil {
		err := fmt.Errorf("a lab already exists for building %q room %q: %q", building, room, l.Name)
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"a lab for building %q room %q already exists (%q) — edit that lab instead",
			building, room, l.Name))
		return err
	}
	return nil
}

// findLabCollision returns the existing lab (other than excludeID) whose
// building and room both exactly match, or nil if there's no such lab.
func findLabCollision(labs []store.Lab, building, room, excludeID string) *store.Lab {
	for i := range labs {
		l := &labs[i]
		if l.ID != excludeID && l.Building == building && l.Room == room {
			return l
		}
	}
	return nil
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
// @Failure      409   {object}  map[string]string
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
	if err := s.checkLabCollision(r.Context(), w, req.Building, req.Room, ""); err != nil {
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
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "lab not found")
		} else {
			s.logger.Error("failed to get lab", "id", id, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to get lab")
		}
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
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      409  {object}  map[string]string
// @Router       /api/v1/labs/{labID} [put]
func (s *Server) UpdateLab(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "labID")
	var req CreateLabRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.checkLabCollision(r.Context(), w, req.Building, req.Room, id); err != nil {
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
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "lab not found")
		} else {
			s.logger.Error("failed to update lab", "id", id, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to update lab")
		}
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
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "lab not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to delete lab")
		}
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
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
