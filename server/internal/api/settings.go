package api

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"

	"github.com/rcobb/openlabstats-server/internal/store"
)

// minAgentVersionPattern matches isVersionBelow's expected dotted-numeric
// shape (e.g. "0.1.10"). Anything else parses as all-zero segments there and
// silently never flags any agent as out of date.
var minAgentVersionPattern = regexp.MustCompile(`^\d+(\.\d+)*$`)

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

// validateSettings returns a non-empty error message if settings has an
// invalid field, or "" if it's acceptable to persist. Split out from
// UpdateSettings so it's unit-testable without an HTTP request or a store.
func validateSettings(settings *store.SystemSettings) string {
	if settings.HeartbeatIntervalSeconds != 0 && settings.HeartbeatIntervalSeconds < 30 {
		return "heartbeatIntervalSeconds must be >= 30"
	}
	// 0 is a valid, documented sentinel meaning "never auto-delete" — see
	// runStaleChecker in cmd/server/main.go, which only calls
	// DeleteStaleAgents when StaleTimeoutDays > 0. Rejecting anything below
	// 1 made that sentinel unreachable through this endpoint: an admin could
	// never actually disable automatic deletion, only ever shorten its
	// window.
	if settings.StaleTimeoutDays < 0 {
		return "staleTimeoutDays must be >= 0 (0 = never auto-delete)"
	}
	if settings.RolloutMaxConcurrent < 0 {
		return "rolloutMaxConcurrent must be >= 0 (0 = unlimited)"
	}
	// No 0 sentinel here: a grace of 0 makes ClaimUpdateSlot's in-flight count
	// always zero, so rolloutMaxConcurrent would never bind.
	if settings.RolloutGraceSeconds < 60 {
		return "rolloutGraceSeconds must be >= 60"
	}
	// Both empty = no restriction; otherwise both must parse, since an
	// unparseable window silently falls back to "always in window" at runtime.
	if (settings.MaintenanceWindowStart != "" || settings.MaintenanceWindowEnd != "") &&
		(!validHHMM(settings.MaintenanceWindowStart) || !validHHMM(settings.MaintenanceWindowEnd)) {
		return "maintenanceWindowStart/End must both be empty or both HH:MM"
	}
	if settings.UpdateIntervalSeconds != 0 && settings.UpdateIntervalSeconds < 60 {
		return "updateIntervalSeconds must be >= 60"
	}
	if settings.MinAgentVersion != "" && !minAgentVersionPattern.MatchString(settings.MinAgentVersion) {
		return `minAgentVersion must look like "0.1.10" (dotted numeric segments)`
	}
	// 0 disables idle exclusion; otherwise bounded to 1 minute .. 24 hours.
	if s := settings.IdleThresholdSeconds; s != 0 && (s < 60 || s > 86400) {
		return "idleThresholdSeconds must be 0 (disabled) or between 60 and 86400"
	}
	return ""
}

// decodeSettingsUpdate decodes a PUT body and reports whether
// idleThresholdSeconds was present. An absent field must keep its stored value:
// a client that predates it (or a stale page resubmitting an old object) would
// otherwise decode it as 0, silently disabling idle exclusion.
func decodeSettingsUpdate(body io.Reader) (settings store.SystemSettings, idlePresent bool, err error) {
	raw, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return settings, false, err
	}
	if err = json.Unmarshal(raw, &settings); err != nil {
		return settings, false, err
	}
	var probe struct {
		Idle *int `json:"idleThresholdSeconds"`
	}
	if err = json.Unmarshal(raw, &probe); err != nil {
		return settings, false, err
	}
	return settings, probe.Idle != nil, nil
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
	defer r.Body.Close()
	settings, idlePresent, err := decodeSettingsUpdate(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if idlePresent {
		// Validate the supplied value; other fields are validated below.
		if msg := validateSettings(&settings); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	} else {
		// Absent: validate everything else with a valid placeholder, then keep
		// the stored value.
		settings.IdleThresholdSeconds = 900
		if msg := validateSettings(&settings); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		current, err := s.store.GetSettings(r.Context())
		if err != nil {
			s.logger.Error("failed to get settings", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to get settings")
			return
		}
		settings.IdleThresholdSeconds = current.IdleThresholdSeconds
	}

	if err := s.store.UpdateSettings(r.Context(), &settings); err != nil {
		s.logger.Error("failed to update settings", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update settings")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
