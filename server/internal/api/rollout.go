package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/rcobb/openlabstats-server/internal/store"
)

// decideRolloutUpdate is the throttled, version-based half of the update
// decision (the manual force-update path is handled separately in
// RegisterAgent and takes priority). It returns the update URL to send this
// agent, or "" to send nothing.
//
// The rollout target is settings.TargetAgentVersion when pinned, otherwise the
// newest installer available for the agent's platform (macOS .pkg vs Windows
// .msi resolve independently). An agent is only ever moved forward
// (isVersionBelow guards direction — no downgrades).
//
// A "slot" is held from the moment an agent is first offered the update until it
// reports a version at/above target (success) or the grace window lapses (assume
// stuck). While in flight the server sends "" rather than re-offering, because
// the deployed old agents have no in-progress guard and would otherwise restack
// installers on every heartbeat.
func (s *Server) decideRolloutUpdate(ctx context.Context, req RegisterAgentRequest, settings *store.SystemSettings) string {
	if settings == nil || !settings.AutoUpdateEnabled {
		return ""
	}

	target := settings.TargetAgentVersion
	if target == "" {
		target = s.GetLatestInstallerVersion(req.OSVersion)
	}

	// No installer to roll toward, or the agent is already at/above target: it's
	// done — release any slot it was holding so the next agent can claim it.
	if target == "" || !isVersionBelow(req.AgentVersion, target) {
		if err := s.store.ReleaseUpdateSlot(ctx, req.ID); err != nil {
			s.logger.Error("failed to release update slot", "agentID", req.ID, "error", err)
		}
		return ""
	}

	// Below target but outside the maintenance window: defer.
	if !isInMaintenanceWindow(settings.MaintenanceWindowStart, settings.MaintenanceWindowEnd, time.Now()) {
		return ""
	}

	grace := time.Duration(settings.RolloutGraceSeconds) * time.Second
	offered, err := s.store.ClaimUpdateSlot(ctx, req.ID, target, settings.RolloutMaxConcurrent, grace)
	if err != nil {
		s.logger.Error("rollout slot claim failed", "agentID", req.ID, "error", err)
		return ""
	}
	if !offered {
		// Renewing an in-flight install, or the budget is full — wait.
		return ""
	}
	url := s.GetLatestInstallerURL(req.OSVersion)
	s.logger.Info("rollout: offering update to agent", "agentID", req.ID, "target", target, "url", url)
	return url
}

// platformLabel returns a coarse human-friendly platform bucket for grouping,
// aligned with findLatestInstaller's detection (macOS .pkg vs Windows .msi).
func platformLabel(osVersion string) string {
	lower := strings.ToLower(osVersion)
	switch {
	case strings.Contains(lower, "macos") || strings.Contains(lower, "darwin"):
		return "macOS"
	case strings.Contains(lower, "windows"):
		return "Windows"
	case isNumericVersion(osVersion):
		return "macOS"
	default:
		return "Windows"
	}
}

// rolloutPlatformStatus is the per-platform rollout progress bucket.
type rolloutPlatformStatus struct {
	Platform  string         `json:"platform"`
	Target    string         `json:"target"`
	Total     int            `json:"total"`
	Updated   int            `json:"updated"`
	Updating  int            `json:"updating"`
	Pending   int            `json:"pending"`
	ByVersion map[string]int `json:"byVersion"`
}

// RolloutStatus godoc
// @Summary      Agent auto-update rollout status
// @Description  Per-platform rollout progress: how many agents are updated, currently updating, or pending, plus the current version distribution and target.
// @Tags         agents
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]string
// @Router       /api/v1/agents/rollout-status [get]
func (s *Server) RolloutStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		s.logger.Error("failed to get settings for rollout status", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get settings")
		return
	}
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		s.logger.Error("failed to list agents for rollout status", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	}

	grace := time.Duration(settings.RolloutGraceSeconds) * time.Second
	now := time.Now()

	// Resolve the rollout target once per distinct OS string (memoized) so we
	// don't re-scan the installer dir for every agent.
	targetForOS := map[string]string{}
	resolveTarget := func(osVersion string) string {
		if settings.TargetAgentVersion != "" {
			return settings.TargetAgentVersion
		}
		if t, ok := targetForOS[osVersion]; ok {
			return t
		}
		t := s.GetLatestInstallerVersion(osVersion)
		targetForOS[osVersion] = t
		return t
	}

	buckets := map[string]*rolloutPlatformStatus{}
	inFlightGlobal := 0
	for i := range agents {
		a := agents[i]
		label := platformLabel(a.OSVersion)
		b := buckets[label]
		if b == nil {
			b = &rolloutPlatformStatus{Platform: label, ByVersion: map[string]int{}}
			buckets[label] = b
		}
		b.Total++
		if a.AgentVersion != "" {
			b.ByVersion[a.AgentVersion]++
		}
		if b.Target == "" {
			b.Target = resolveTarget(a.OSVersion)
		}

		target := resolveTarget(a.OSVersion)
		switch {
		case target == "" || !isVersionBelow(a.AgentVersion, target):
			b.Updated++
		case a.UpdateOfferedAt != nil && a.UpdateOfferedAt.After(now.Add(-grace)):
			b.Updating++
			inFlightGlobal++
		default:
			b.Pending++
		}
	}

	platforms := make([]*rolloutPlatformStatus, 0, len(buckets))
	for _, b := range buckets {
		platforms = append(platforms, b)
	}
	sort.Slice(platforms, func(i, j int) bool { return platforms[i].Platform < platforms[j].Platform })

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"autoUpdateEnabled":  settings.AutoUpdateEnabled,
		"maxConcurrent":      settings.RolloutMaxConcurrent,
		"gracePeriodSeconds": settings.RolloutGraceSeconds,
		"targetPin":          settings.TargetAgentVersion,
		"inFlightGlobal":     inFlightGlobal,
		"platforms":          platforms,
	})
}

// isInMaintenanceWindow reports whether `now` falls inside the HH:mm..HH:mm
// window. It mirrors the agent's IsInMaintenanceWindow
// (agent/internal/enrollment/client.go) so the server and agent agree on the
// rule; the agent module can't be imported here, so the logic is duplicated.
//
// Semantics: an empty start or end means "always in window" (no restriction —
// matches the historical behavior of an unset window). A zero-length window
// (start == end) means "never". Windows that wrap past midnight (e.g.
// 22:00..04:00) are supported. Uses server-local time, which is correct for a
// single-timezone fleet.
func isInMaintenanceWindow(startStr, endStr string, now time.Time) bool {
	if startStr == "" || endStr == "" {
		return true
	}
	parseHHMM := func(s string) (int, bool) {
		var h, m int
		if n, _ := fmt.Sscanf(s, "%d:%d", &h, &m); n != 2 {
			return 0, false
		}
		if h < 0 || h > 23 || m < 0 || m > 59 {
			return 0, false
		}
		return h*60 + m, true
	}
	startMinutes, ok1 := parseHHMM(startStr)
	endMinutes, ok2 := parseHHMM(endStr)
	if !ok1 || !ok2 {
		return true // treat invalid config as always in window (safe default)
	}
	if startMinutes == endMinutes {
		return false // zero-length window means never
	}
	currentMinutes := now.Hour()*60 + now.Minute()
	if startMinutes < endMinutes {
		return currentMinutes >= startMinutes && currentMinutes <= endMinutes
	}
	// Wraps midnight: e.g. 23:00–05:00
	return currentMinutes >= startMinutes || currentMinutes <= endMinutes
}
