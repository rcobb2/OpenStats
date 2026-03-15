package api

import (
	"net/http"

	"github.com/rcobb/openlabstats-server/internal/store"
)

// GetSettings godoc
// @Summary      Get system settings
// @Description  Returns global configuration for agents and server.
// @Tags         settings
// @Produce      json
// @Success      200  {object}  store.SystemSettings
// @Failure      500  {object}  map[string]string
// @Router       /api/v1/settings [get]
func (s *Server) GetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetSettings(r.Context())
	if err != nil {
		s.logger.Error("failed to get settings", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get settings")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// UpdateSettings godoc
// @Summary      Update system settings
// @Description  Updates global configuration for agents and server.
// @Tags         settings
// @Accept       json
// @Produce      json
// @Param        body  body  store.SystemSettings  true  "Settings payload"
// @Success      200   {object}  map[string]string
// @Failure      400   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/v1/settings [put]
func (s *Server) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings store.SystemSettings
	if err := readJSON(r, &settings); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.store.UpdateSettings(r.Context(), &settings); err != nil {
		s.logger.Error("failed to update settings", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update settings")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
