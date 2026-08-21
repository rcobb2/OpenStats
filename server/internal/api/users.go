package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/rcobb/openlabstats-server/internal/store"
	"github.com/rcobb/openlabstats-server/internal/userid"
)

// UserMappingRequest is the payload for creating or updating a user rule.
type UserMappingRequest struct {
	Pattern       string `json:"pattern"`
	CanonicalUser string `json:"canonicalUser"`
	DisplayName   string `json:"displayName"`
	Notes         string `json:"notes"`
	Ignored       bool   `json:"ignored"`
}

// AgentUserPolicy is the user-tracking policy pushed to agents. Agents apply it
// at collection time: ignored accounts never produce metrics, and the canonical
// username becomes the `user` label so that a domain account and its macOS
// counterpart share one time series.
type AgentUserPolicy struct {
	StripDomain    bool              `json:"stripDomain"`
	IgnorePatterns []string          `json:"ignorePatterns"`
	Aliases        map[string]string `json:"aliases"`
}

// DiscoveredUser is one identity as it appears in reports, with the raw
// usernames that were merged into it.
type DiscoveredUser struct {
	CanonicalUser string   `json:"canonicalUser"`
	DisplayName   string   `json:"displayName,omitempty"`
	RawUsers      []string `json:"rawUsers"`
	Ignored       bool     `json:"ignored"`
	MatchedRule   string   `json:"matchedRule,omitempty"`
	RuleID        int      `json:"ruleId,omitempty"`
	ActiveNow     bool     `json:"activeNow"`
	SessionHours  float64  `json:"sessionHours"`
}

// userPolicy builds the effective policy from the DB. On error it returns the
// defaults so that a database hiccup degrades to built-in filtering rather than
// leaking service accounts into reports.
func (s *Server) userPolicy(ctx context.Context) *userid.Policy {
	policy := userid.NewPolicy()

	strip, err := s.store.GetUserStripDomain(ctx)
	if err != nil {
		s.logger.Warn("failed to read user strip-domain setting", "error", err)
	} else {
		policy.StripDomain = strip
	}

	mappings, err := s.store.ListUserMappings(ctx)
	if err != nil {
		s.logger.Warn("failed to load user mappings", "error", err)
		return policy
	}
	policy.Rules = make([]userid.Rule, 0, len(mappings))
	for _, m := range mappings {
		policy.Rules = append(policy.Rules, userid.Rule{
			Pattern:   m.Pattern,
			Canonical: m.CanonicalUser,
			Ignored:   m.Ignored,
		})
	}
	return policy
}

// ListUserMappings godoc
// @Summary      List user rules
// @Description  Returns all user ignore/correlation rules.
// @Tags         users
// @Produce      json
// @Success      200  {array}  store.UserMapping
// @Router       /api/v1/users/mappings [get]
func (s *Server) ListUserMappings(w http.ResponseWriter, r *http.Request) {
	mappings, err := s.store.ListUserMappings(r.Context())
	if err != nil {
		s.logger.Error("failed to list user mappings", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list user mappings")
		return
	}
	if mappings == nil {
		mappings = []store.UserMapping{}
	}
	writeJSON(w, http.StatusOK, mappings)
}

// UpsertUserMapping godoc
// @Summary      Create or update a user rule
// @Description  Adds or replaces the rule for a username pattern. Patterns may contain "*" wildcards and match case-insensitively against the raw username and its canonical form.
// @Tags         users
// @Accept       json
// @Produce      json
// @Param        body  body  UserMappingRequest  true  "Rule details"
// @Success      200   {object}  map[string]string
// @Failure      400   {object}  map[string]string
// @Router       /api/v1/users/mappings [put]
func (s *Server) UpsertUserMapping(w http.ResponseWriter, r *http.Request) {
	var req UserMappingRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Pattern = strings.TrimSpace(req.Pattern)
	req.CanonicalUser = strings.TrimSpace(req.CanonicalUser)
	if req.Pattern == "" {
		writeError(w, http.StatusBadRequest, "pattern is required")
		return
	}
	if !validUserPattern(req.Pattern) {
		writeError(w, http.StatusBadRequest, `pattern may only contain letters, digits, and . _ - $ @ * \ or /`)
		return
	}
	if !req.Ignored && req.CanonicalUser == "" {
		writeError(w, http.StatusBadRequest, "canonicalUser is required unless the rule is an ignore rule")
		return
	}
	if req.CanonicalUser != "" && !validUserPattern(req.CanonicalUser) {
		writeError(w, http.StatusBadRequest, "canonicalUser contains invalid characters")
		return
	}

	m := &store.UserMapping{
		Pattern:       req.Pattern,
		CanonicalUser: req.CanonicalUser,
		DisplayName:   req.DisplayName,
		Notes:         req.Notes,
		Source:        "manual",
		Ignored:       req.Ignored,
	}
	if err := s.store.UpsertUserMapping(r.Context(), m); err != nil {
		s.logger.Error("failed to save user mapping", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save user rule")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// DeleteUserMapping godoc
// @Summary      Delete a user rule
// @Tags         users
// @Produce      json
// @Param        mappingID  path  int  true  "Rule ID"
// @Success      200  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /api/v1/users/mappings/{mappingID} [delete]
func (s *Server) DeleteUserMapping(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "mappingID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rule id")
		return
	}
	if err := s.store.DeleteUserMapping(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete rule")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ToggleUserMappingIgnored godoc
// @Summary      Toggle a user rule's ignored flag
// @Tags         users
// @Accept       json
// @Produce      json
// @Param        mappingID  path  int  true  "Rule ID"
// @Success      200  {object}  map[string]string
// @Router       /api/v1/users/mappings/{mappingID}/ignore [patch]
func (s *Server) ToggleUserMappingIgnored(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "mappingID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rule id")
		return
	}
	var body struct {
		Ignored bool `json:"ignored"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.store.SetUserMappingIgnored(r.Context(), id, body.Ignored); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update rule")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// QuickIgnoreUser godoc
// @Summary      Ignore a user
// @Description  Marks a username as ignored, creating the rule if needed. Used by the ignore action on the users and reports pages.
// @Tags         users
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  map[string]string
// @Router       /api/v1/users/ignore [post]
func (s *Server) QuickIgnoreUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User  string `json:"user"`
		Notes string `json:"notes"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.User = strings.TrimSpace(body.User)
	if body.User == "" {
		writeError(w, http.StatusBadRequest, "user is required")
		return
	}
	if !validUserPattern(body.User) {
		writeError(w, http.StatusBadRequest, "user contains invalid characters")
		return
	}
	if err := s.store.IgnoreUserPattern(r.Context(), body.User, body.Notes); err != nil {
		s.logger.Error("failed to ignore user", "user", body.User, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to ignore user")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
}

// GetUserPolicy godoc
// @Summary      Get the effective user policy
// @Description  Returns the policy agents apply at collection time: domain stripping, ignore patterns, and alias mappings. Agents also receive this in the registration response.
// @Tags         users
// @Produce      json
// @Success      200  {object}  AgentUserPolicy
// @Router       /api/v1/users/policy [get]
func (s *Server) GetUserPolicy(w http.ResponseWriter, r *http.Request) {
	policy, err := s.buildAgentUserPolicy(r.Context())
	if err != nil {
		s.logger.Error("failed to build user policy", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to build user policy")
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

// UpdateUserPolicy godoc
// @Summary      Update user policy options
// @Description  Sets whether domain prefixes and UPN suffixes are stripped when correlating identities across Windows and macOS.
// @Tags         users
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]string
// @Router       /api/v1/users/policy [put]
func (s *Server) UpdateUserPolicy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		StripDomain *bool `json:"stripDomain"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.StripDomain == nil {
		writeError(w, http.StatusBadRequest, "stripDomain is required")
		return
	}
	if err := s.store.SetUserStripDomain(r.Context(), *body.StripDomain); err != nil {
		s.logger.Error("failed to update user policy", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update user policy")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// buildAgentUserPolicy converts DB rules into the agent-facing payload.
func (s *Server) buildAgentUserPolicy(ctx context.Context) (*AgentUserPolicy, error) {
	strip, err := s.store.GetUserStripDomain(ctx)
	if err != nil {
		return nil, err
	}
	mappings, err := s.store.ListUserMappings(ctx)
	if err != nil {
		return nil, err
	}

	out := &AgentUserPolicy{
		StripDomain:    strip,
		IgnorePatterns: []string{},
		Aliases:        map[string]string{},
	}
	for _, m := range mappings {
		if m.Ignored {
			out.IgnorePatterns = append(out.IgnorePatterns, m.Pattern)
			continue
		}
		if m.CanonicalUser != "" {
			out.Aliases[m.Pattern] = m.CanonicalUser
		}
	}
	return out, nil
}

// ListDiscoveredUsers godoc
// @Summary      List users seen in metrics
// @Description  Returns every username Prometheus has recorded, grouped by the canonical identity it resolves to, with its ignore state and recent session hours.
// @Tags         users
// @Produce      json
// @Param        range  query  string  false  "Time range for session hours"  default(30d)
// @Success      200  {array}  DiscoveredUser
// @Router       /api/v1/users [get]
func (s *Server) ListDiscoveredUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	timeRange := safeTimeRange(r.URL.Query().Get("range"), "30d")

	policy := s.userPolicy(ctx)

	rawUsers, err := s.fetchUserLabelValues(ctx)
	if err != nil {
		s.logger.Error("failed to fetch user label values", "error", err)
		writeError(w, http.StatusBadGateway, "failed to reach Prometheus")
		return
	}

	// Recent session hours and current activity, both keyed by raw username.
	hoursByRaw := s.instantQueryByUser(ctx,
		fmt.Sprintf(`sum by (user) (increase(openlabstats_user_session_seconds_total{user!=""}[%s])) / 3600`, timeRange))
	activeByRaw := s.instantQueryByUser(ctx, `sum by (user) (openlabstats_user_session_active{user!=""})`)

	// A raw user only present in one of the sources still deserves a row.
	for raw := range hoursByRaw {
		rawUsers = append(rawUsers, raw)
	}
	for raw := range activeByRaw {
		rawUsers = append(rawUsers, raw)
	}

	// Rules whose pattern names a user Prometheus has never seen (typed ahead of
	// time, or whose series has expired) should still be visible and editable.
	mappings, err := s.store.ListUserMappings(ctx)
	if err != nil {
		s.logger.Error("failed to load user mappings", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load user rules")
		return
	}
	ruleByPattern := make(map[string]store.UserMapping, len(mappings))
	for _, m := range mappings {
		ruleByPattern[m.Pattern] = m
		if !strings.Contains(m.Pattern, "*") {
			rawUsers = append(rawUsers, m.Pattern)
		}
	}

	grouped := make(map[string]*DiscoveredUser)
	// An identity counts as ignored only when every raw form of it is ignored, so
	// a rule that ignores one domain's copy does not hide the real user.
	ignoredCount := make(map[string]int)
	seenRaw := make(map[string]bool)
	for _, raw := range rawUsers {
		if raw == "" || seenRaw[strings.ToLower(raw)] {
			continue
		}
		seenRaw[strings.ToLower(raw)] = true

		canonical, ignored := policy.Resolve(raw)
		if canonical == "" {
			continue
		}

		entry, ok := grouped[canonical]
		if !ok {
			entry = &DiscoveredUser{CanonicalUser: canonical}
			grouped[canonical] = entry
		}
		entry.RawUsers = append(entry.RawUsers, raw)
		if ignored {
			ignoredCount[canonical]++
		}
		entry.SessionHours += hoursByRaw[raw]
		if activeByRaw[raw] > 0 {
			entry.ActiveNow = true
		}
		if rule, ok := ruleByPattern[strings.ToLower(raw)]; ok {
			entry.MatchedRule = rule.Pattern
			entry.RuleID = rule.ID
			if rule.DisplayName != "" {
				entry.DisplayName = rule.DisplayName
			}
		}
	}

	// A rule keyed on the canonical name (the common case for a merge rule)
	// attaches to the group even when no raw username equals it.
	out := make([]DiscoveredUser, 0, len(grouped))
	for canonical, entry := range grouped {
		entry.Ignored = ignoredCount[canonical] == len(entry.RawUsers)
		if entry.MatchedRule == "" {
			if rule, ok := ruleByPattern[canonical]; ok {
				entry.MatchedRule = rule.Pattern
				entry.RuleID = rule.ID
				if rule.DisplayName != "" {
					entry.DisplayName = rule.DisplayName
				}
			}
		}
		sort.Strings(entry.RawUsers)
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ignored != out[j].Ignored {
			return !out[i].Ignored
		}
		if out[i].SessionHours != out[j].SessionHours {
			return out[i].SessionHours > out[j].SessionHours
		}
		return out[i].CanonicalUser < out[j].CanonicalUser
	})
	writeJSON(w, http.StatusOK, out)
}

// fetchUserLabelValues returns every value Prometheus holds for the `user` label.
func (s *Server) fetchUserLabelValues(ctx context.Context) ([]string, error) {
	promURL := s.cfg.Prom.URL + "/api/v1/label/user/values"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, promURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.promClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus returned %d", resp.StatusCode)
	}
	var body struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Data, nil
}

// instantQueryByUser runs an instant query and reduces it to user → value.
// Query failures yield an empty map: the users page is still useful without
// the session-hours column.
func (s *Server) instantQueryByUser(ctx context.Context, query string) map[string]float64 {
	promURL := fmt.Sprintf("%s/api/v1/query?query=%s", s.cfg.Prom.URL, url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, promURL, nil)
	if err != nil {
		return map[string]float64{}
	}
	resp, err := s.promClient.Do(req)
	if err != nil {
		s.logger.Warn("prometheus query failed", "error", err, "query", query)
		return map[string]float64{}
	}
	defer resp.Body.Close()

	var result promQueryInstantResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		s.logger.Warn("failed to decode prometheus response", "error", err)
		return map[string]float64{}
	}

	out := make(map[string]float64, len(result.Data.Result))
	for _, entry := range result.Data.Result {
		user := entry.Metric["user"]
		if user == "" || len(entry.Value) < 2 {
			continue
		}
		v, ok := entry.Value[1].(string)
		if !ok {
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			continue
		}
		out[user] += f
	}
	return out
}

// validUserPattern rejects characters that cannot appear in a username, which
// keeps admin input out of PromQL regex syntax.
func validUserPattern(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-' || c == '$' || c == '@' || c == '*' || c == '\\' || c == '/' || c == ' ':
		default:
			return false
		}
	}
	return true
}
