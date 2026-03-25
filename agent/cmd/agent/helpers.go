package main

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/rcobb/openlabstats-agent/internal/inventory"
	"github.com/rcobb/openlabstats-agent/internal/metrics"
	"github.com/rcobb/openlabstats-agent/internal/monitor"
	"github.com/rcobb/openlabstats-agent/internal/normalizer"
	"github.com/rcobb/openlabstats-agent/internal/store"
)

func restoreMetrics(db *store.Store, m *metrics.Metrics, logger *slog.Logger) error {
	totals, err := db.GetUsageTotals()
	if err != nil {
		return err
	}

	for _, t := range totals {
		// Skip entries with exe names as users (legacy data from Caption bug).
		if !isValidUser(t.User) {
			t.User = ""
		}
		labels := []string{t.DisplayName, t.ExeName, t.Category, t.User, t.Hostname}
		m.AppUsageSeconds.WithLabelValues(labels...).Add(t.TotalSeconds)
		m.AppForegroundSeconds.WithLabelValues(labels...).Add(t.TotalForegroundSeconds)
		m.AppLaunches.WithLabelValues(labels...).Add(float64(t.TotalLaunches))
	}

	logger.Info("restored metrics from store", "entries", len(totals))
	return nil
}

// checkpointSessions flushes elapsed time for all active user sessions into the
// counter and updates the live duration gauge. Called from runCheckpointLoop so
// that active sessions appear in Prometheus even before they end.
func (usm *userSessionManager) checkpointSessions() {
	usm.mu.Lock()
	defer usm.mu.Unlock()

	hostname := metrics.Hostname()
	now := time.Now()

	for user, state := range usm.users {
		elapsed := now.Sub(state.lastCheckpoint).Seconds()
		state.lastCheckpoint = now
		usm.metrics.UserSessionSecondsTotal.WithLabelValues(user, hostname).Add(elapsed)
		usm.metrics.UserSessionDuration.WithLabelValues(user, hostname).Set(now.Sub(state.loginTime).Seconds())
	}
}

func runCheckpointLoop(ctx context.Context, tracker *monitor.Tracker, norm *normalizer.Normalizer, m *metrics.Metrics, userSessions *userSessionManager, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			userSessions.checkpointSessions()
			snapshots := tracker.CheckpointActive()
			hostname := metrics.Hostname()

			// Deduplicate metrics by label set.
			// This prevents multiple process groups of the same app for the same user
			// from artificially multiplying the usage time.
			type usageKey struct {
				DisplayName string
				ExeName     string
				Category    string
				User        string
			}

			usageSeconds := make(map[usageKey]float64)
			foregroundSeconds := make(map[usageKey]float64)

			for _, s := range snapshots {
				// Only checkpoint metrics for valid human users.
				if !isValidUser(s.User) {
					continue
				}

				info := norm.Resolve(s.ExeName, s.ExePath)
				key := usageKey{
					DisplayName: info.DisplayName,
					ExeName:     s.ExeName,
					Category:    info.Category,
					User:        s.User,
				}

				// For total usage, count the checkpoint interval ONCE per unique app/user/host.
				usageSeconds[key] = s.CheckpointDelta.Seconds()

				// For foreground time, only one PID should have a delta anyway, but we sum
				// to be safe (in case rapid switching happened within the checkpoint window).
				foregroundSeconds[key] += s.ForegroundDelta.Seconds()
			}

			for key, seconds := range usageSeconds {
				m.AppUsageSeconds.WithLabelValues(key.DisplayName, key.ExeName, key.Category, key.User, hostname).Add(seconds)
			}
			for key, seconds := range foregroundSeconds {
				if seconds > 0 {
					m.AppUsageSeconds.WithLabelValues(key.DisplayName, key.ExeName, key.Category, key.User, hostname).Add(0) // Ensure metric initialized
					m.AppForegroundSeconds.WithLabelValues(key.DisplayName, key.ExeName, key.Category, key.User, hostname).Add(seconds)
				}
			}
		}
	}
}

func runInventoryLoop(ctx context.Context, scanner *inventory.Scanner, m *metrics.Metrics, interval time.Duration, logger *slog.Logger) {
	// Run immediately on startup.
	updateInventoryMetrics(scanner, m)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			updateInventoryMetrics(scanner, m)
		}
	}
}

func updateInventoryMetrics(scanner *inventory.Scanner, m *metrics.Metrics) {
	apps := scanner.Scan()
	hostname := metrics.Hostname()

	// Reset and repopulate.
	m.InstalledSoftware.Reset()
	for _, app := range apps {
		m.InstalledSoftware.WithLabelValues(app.Name, app.Version, app.Publisher, hostname).Set(1)
	}
}

func runMappingRefreshLoop(ctx context.Context, mapping *normalizer.MappingFile, norm *normalizer.Normalizer, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := mapping.Reload(); err != nil {
				logger.Warn("failed to reload mapping file", "error", err)
			} else {
				norm.ClearCache()
			}
		}
	}
}

// isValidUser returns true if the string looks like a real human user
// rather than a system account, service, or process name.
func isValidUser(user string) bool {
	if user == "" {
		return false
	}
	lower := strings.ToLower(user)

	// 1. Filter out common system/service/technical accounts.
	blacklist := []string{
		"system",
		"local service",
		"network service",
		"window manager",
		"trustedinstaller",
		"font driver host",
		"dwm",
		"umfd",
		"usermode font driver",
		"anonymous logon",
		"local system",
		"iusr",
		"iwam",
		"mssqlserver",
		"postgres",
		"mysql",
		"service",
		"localsystem",
		"networkservice",
		"localservice",
		// macOS system accounts
		"root",
		"daemon",
		"nobody",
		"wheel",
	}

	// Check for exact match or suffix (to handle DOMAIN\Account or NT AUTHORITY\Account)
	for _, b := range blacklist {
		if lower == b || strings.HasSuffix(lower, "\\"+b) {
			return false
		}
	}

	// 2. Filter out anything under NT AUTHORITY or NT SERVICE domains.
	if strings.Contains(lower, "nt authority") || strings.Contains(lower, "nt service") {
		return false
	}

	// 3. Filter out Computer accounts (AccountName ends in $)
	if strings.HasSuffix(lower, "$") {
		return false
	}

	// 4. Reject anything that looks like an executable or system binary.
	for _, suffix := range []string{".exe", ".dll", ".sys", ".com", ".msc", ".scr", ".bat", ".cmd"} {
		if strings.HasSuffix(lower, suffix) {
			return false
		}
	}

	// 5. macOS Apple service accounts use underscore prefix (_www, _spotlight, etc.)
	if strings.HasPrefix(lower, "_") {
		return false
	}

	// 6. Minimal length check (human usernames are usually at least 2 chars).
	if len(user) < 2 {
		return false
	}

	return true
}

// userSessionManager derives user login/logoff events from process start/stop events.
// When a user's first tracked process appears, it counts as a login.
// When a user's last tracked process ends, it counts as a logoff.
type userSessionManager struct {
	mu      sync.Mutex
	users   map[string]*userState // user -> state
	metrics *metrics.Metrics
	logger  *slog.Logger
}

type userState struct {
	refCount       int // number of active tracked process groups for this user
	loginTime      time.Time
	lastCheckpoint time.Time
}

func newUserSessionManager(m *metrics.Metrics, logger *slog.Logger) *userSessionManager {
	return &userSessionManager{
		users:   make(map[string]*userState),
		metrics: m,
		logger:  logger,
	}
}

func (usm *userSessionManager) onProcessStart(user string, pid uint32) {
	usm.mu.Lock()
	defer usm.mu.Unlock()

	hostname := metrics.Hostname()

	state, exists := usm.users[user]
	if !exists {
		// First process for this user — counts as a login.
		now := time.Now()
		state = &userState{loginTime: now, lastCheckpoint: now}
		usm.users[user] = state

		usm.metrics.UserSessionLogins.WithLabelValues(user, hostname).Inc()
		usm.metrics.UserSessionActive.WithLabelValues(user, hostname).Set(1)
		usm.logger.Info("user session started", "user", user)
	}

	state.refCount++
}

func (usm *userSessionManager) onProcessStop(user string, pid uint32) {
	usm.mu.Lock()
	defer usm.mu.Unlock()

	hostname := metrics.Hostname()

	state, exists := usm.users[user]
	if !exists {
		return
	}

	state.refCount--

	if state.refCount <= 0 {
		// Last process ended — counts as a logoff.
		// Only add time since last checkpoint to avoid double-counting periodic flushes.
		duration := time.Since(state.lastCheckpoint).Seconds()
		usm.metrics.UserSessionSecondsTotal.WithLabelValues(user, hostname).Add(duration)
		usm.metrics.UserSessionActive.WithLabelValues(user, hostname).Set(0)
		usm.metrics.UserSessionDuration.WithLabelValues(user, hostname).Set(0)
		delete(usm.users, user)
		usm.logger.Info("user session ended", "user", user, "duration", duration)
	} else {
		// Update current session duration gauge.
		usm.metrics.UserSessionDuration.WithLabelValues(user, hostname).Set(time.Since(state.loginTime).Seconds())
	}
}
