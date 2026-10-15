package main

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/rcobb/openlabstats-agent/internal/enrollment"
	"github.com/rcobb/openlabstats-agent/internal/inventory"
	"github.com/rcobb/openlabstats-agent/internal/logon"
	"github.com/rcobb/openlabstats-agent/internal/metrics"
	"github.com/rcobb/openlabstats-agent/internal/monitor"
	"github.com/rcobb/openlabstats-agent/internal/normalizer"
	"github.com/rcobb/openlabstats-agent/internal/session"
	"github.com/rcobb/openlabstats-agent/internal/userid"
)

func runCheckpointLoop(ctx context.Context, tracker *monitor.Tracker, norm *normalizer.Normalizer, m *metrics.Metrics, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
				// Only checkpoint metrics for valid human users, under the canonical
				// username — two process groups owned by the same person under
				// different name forms must land on one key, not two.
				user, ok := resolveUser(s.User)
				if !ok {
					continue
				}

				info := norm.Resolve(s.ExeName, s.ExePath)
				key := usageKey{
					DisplayName: info.DisplayName,
					ExeName:     s.ExeName,
					Category:    info.Category,
					User:        user,
				}

				// For total usage, count the checkpoint interval ONCE per unique app/user/host.
				// First-wins: if two groups resolve to the same key, don't double-count.
				if _, exists := usageSeconds[key]; !exists {
					usageSeconds[key] = s.CheckpointDelta.Seconds()
				}

				// For foreground time, only one PID should have a delta anyway, but we sum
				// to be safe (in case rapid switching happened within the checkpoint window).
				foregroundSeconds[key] += s.ForegroundDelta.Seconds()
			}

			for key, seconds := range usageSeconds {
				m.AppUsageSeconds.Add(seconds, key.DisplayName, key.ExeName, key.Category, key.User, hostname)
			}
			for key, seconds := range foregroundSeconds {
				if seconds > 0 {
					m.AppForegroundSeconds.Add(seconds, key.DisplayName, key.ExeName, key.Category, key.User, hostname)
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

// userPolicy holds the server-managed user policy: which accounts to ignore and
// how to merge usernames that belong to one person. It starts at the built-in
// defaults and is replaced on every heartbeat.
var userPolicy = userid.NewHolder()

// builtinUserPolicy is the default rule set, with no server-managed rules
// applied. It backs isValidUser so the built-in account filtering lives in
// exactly one place (internal/userid).
var builtinUserPolicy = userid.NewPolicy()

// applyUserPolicy installs a policy received from the server.
func applyUserPolicy(p *enrollment.UserPolicy, logger *slog.Logger) {
	if p == nil {
		return
	}
	userPolicy.Set(userid.FromServer(p.StripDomain, p.IgnorePatterns, p.Aliases))
	logger.Info("applied user policy",
		"stripDomain", p.StripDomain,
		"ignoredUsers", len(p.IgnorePatterns),
		"aliases", len(p.Aliases))
}

// bootstrapUserPolicy fetches the policy before process scanning starts, so that
// already-running processes are attributed to the right identity instead of
// waiting for the first heartbeat. A failure here is not fatal: the built-in
// defaults apply until a heartbeat succeeds.
func bootstrapUserPolicy(ctx context.Context, client *enrollment.Client, logger *slog.Logger) {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	policy, err := client.FetchUserPolicy(fetchCtx)
	if err != nil {
		logger.Warn("failed to fetch user policy, using built-in defaults", "error", err)
		return
	}
	applyUserPolicy(policy, logger)
}

// newLogonTracker builds the OS-logon session tracker. It owns the user-session
// metrics outright: sessions come from the OS (WTS on Windows, utmpx on macOS)
// rather than being inferred from process refcounts, so a sign-in registers even
// when another account already has processes running, and an agent restart no
// longer manufactures a logon for every session that was already open.
func newLogonTracker(m *metrics.Metrics, logger *slog.Logger) *logon.Tracker {
	return logon.NewTracker(logon.NewEnumerator(), m, resolveUser, metrics.Hostname(), logger)
}

// resolveUser canonicalizes a raw OS username and reports whether it should be
// tracked. The canonical form is what goes into metric labels, so a Windows
// domain account (COLGATE\jdoe) and its macOS counterpart (jdoe) share one
// series. Callers must use the returned name, not the raw one.
func resolveUser(raw string) (string, bool) {
	canonical, ignored := userPolicy.Resolve(raw)
	if ignored || canonical == "" {
		return "", false
	}
	return canonical, true
}

// isValidUser returns true if the string looks like a real human user rather
// than a system account, service, or process name. It applies only the built-in
// rules — resolveUser is what callers should use, since it also applies the
// server-managed policy and returns the canonical name.
func isValidUser(user string) bool {
	_, ignored := builtinUserPolicy.Resolve(user)
	return !ignored
}

// idleThreshold is the live idle cutoff. It starts at the configured value and
// is overridden by the server's idleThresholdSeconds on every registration, so
// an admin change applies without restarting agents.
type idleThreshold struct {
	configured time.Duration
	current    atomic.Int64 // time.Duration
	logger     *slog.Logger
}

func newIdleThreshold(configured time.Duration, logger *slog.Logger) *idleThreshold {
	t := &idleThreshold{configured: configured, logger: logger}
	t.current.Store(int64(configured))
	return t
}

// Get returns the current threshold; 0 means idle exclusion is disabled.
func (t *idleThreshold) Get() time.Duration { return time.Duration(t.current.Load()) }

// Apply folds the server's settings in. An absent value (older server) means
// the local config value; 0 disables idle exclusion.
func (t *idleThreshold) Apply(s *enrollment.SystemSettings) {
	next := t.configured
	if s.IdleThresholdSeconds != nil {
		next = time.Duration(*s.IdleThresholdSeconds) * time.Second
	}
	if next < 0 {
		next = 0
	}
	if old := time.Duration(t.current.Swap(int64(next))); old != next {
		t.logger.Info("idle threshold changed", "old", old, "new", next)
	}
}

// startForegroundTracking starts the session-helper supervisor, the helper
// health gauge, and the foreground poller that consumes the helpers' samples.
func startForegroundTracking(ctx context.Context, tracker *monitor.Tracker, m *metrics.Metrics, idle *idleThreshold, logger *slog.Logger) {
	hub := session.NewHub()
	go session.NewSupervisor(hub, logger).Run(ctx)

	hostname := metrics.Hostname()
	connected := m.SessionHelpersConnected.WithLabelValues(hostname)
	connected.Set(0)
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				connected.Set(float64(hub.Connected()))
			}
		}
	}()

	go monitor.RunForegroundPoller(ctx, tracker, monitor.ForegroundPollerConfig{
		Interval:      1 * time.Second,
		Source:        hub.Fresh,
		IdleThreshold: idle.Get,
		OnIdle:        func(d time.Duration) { m.ForegroundIdleSeconds.Add(d.Seconds(), hostname) },
	}, logger)
}
