package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/rcobb/openlabstats-agent/internal/config"
	"github.com/rcobb/openlabstats-agent/internal/enrollment"
	"github.com/rcobb/openlabstats-agent/internal/inventory"
	"github.com/rcobb/openlabstats-agent/internal/metrics"
	"github.com/rcobb/openlabstats-agent/internal/monitor"
	"github.com/rcobb/openlabstats-agent/internal/normalizer"
	"github.com/rcobb/openlabstats-agent/internal/service"
	"github.com/rcobb/openlabstats-agent/internal/store"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			exePath, _ := os.Executable()
			if err := service.Install(exePath); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to install service: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Service installed successfully.")
			return

		case "uninstall":
			if err := service.Uninstall(); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to uninstall service: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Service uninstalled successfully.")
			return

		case "version":
			fmt.Println("openlabstats-agent v0.1.0")
			return

		case "help":
			printUsage()
			return
		}
	}

	// Determine config path.
	configPath := ""
	for i, arg := range os.Args {
		if arg == "--config" && i+1 < len(os.Args) {
			configPath = os.Args[i+1]
		}
	}

	// Load configuration.
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Set up logging.
	logger := setupLogger(cfg)

	// Run the agent (either as service or console).
	if err := service.Run(runAgent(cfg, logger), logger); err != nil {
		logger.Error("agent failed", "error", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`OpenLabStats Agent - Software usage tracking for higher education

Usage:
  openlabstats-agent [command] [options]

Commands:
  install     Install as a Windows service
  uninstall   Uninstall the Windows service
  version     Print version information
  help        Show this help message

Options:
  --config <path>   Path to configuration file (default: configs/agent.yaml)

Running without a command starts the agent (as service or console).`)
}

func setupLogger(cfg *config.Config) *slog.Logger {
	// Parse log level.
	var level slog.Level
	switch cfg.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	// Create log file if configured.
	var writers []io.Writer
	writers = append(writers, os.Stdout)

	if cfg.Logging.FilePath != "" {
		dir := filepath.Dir(cfg.Logging.FilePath)
		os.MkdirAll(dir, 0755)
		f, err := os.OpenFile(cfg.Logging.FilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			writers = append(writers, f)
		}
	}

	handler := slog.NewJSONHandler(io.MultiWriter(writers...), &slog.HandlerOptions{
		Level: level,
	})
	return slog.New(handler)
}

// runAgent returns a function that runs the full agent lifecycle.
func runAgent(cfg *config.Config, logger *slog.Logger) service.AgentRunner {
	return func(ctx context.Context) error {
		logger.Info("starting OpenLabStats agent", "version", "0.1.0")

		// Initialize metrics.
		m := metrics.New()

		// Initialize local store.
		db, err := store.New(cfg.Store.DBPath, logger)
		if err != nil {
			return fmt.Errorf("failed to initialize store: %w", err)
		}
		defer db.Close()

		// Restore metrics from persisted totals.
		if err := restoreMetrics(db, m, logger); err != nil {
			logger.Warn("failed to restore metrics from store", "error", err)
		}

		// Initialize normalizer.
		mapping, err := normalizer.NewMappingFile(cfg.Normalizer.MappingFile, logger)
		if err != nil {
			logger.Warn("failed to load mapping file, continuing without it", "error", err)
			mapping = nil
		}
		pe := normalizer.NewPEReader(logger)
		norm := normalizer.NewNormalizer(mapping, pe, logger)

		// Initialize process tracker.
		tracker := monitor.NewTracker(logger)

		// Initialize user session manager (derives login/logoff from process events).
		userSessions := newUserSessionManager(m, logger)

		// Set up WMI watcher.
		watcher, err := monitor.NewWMIWatcher(tracker, logger, monitor.WMIWatcherConfig{
			ExcludePatterns: cfg.Monitor.ExcludePatterns,
			MinLifetime:     cfg.Monitor.MinLifetime,
			FamilyResolver:  norm.ResolveFamily,
			OnStart: func(pid uint32, exeName string, isNewGroup bool) {
				if !isNewGroup {
					return // child process joined existing group, skip
				}
				// Track user session from process owner.
				user := tracker.GetProcessUser(pid)
				if isValidUser(user) {
					userSessions.onProcessStart(user, pid)
				}
			},
			OnStop: func(session *monitor.ProcessSession) {
				// Resolve the friendly name and record the session.
				info := norm.Resolve(session.ExeName, session.ExePath)
				hostname := metrics.Hostname()

				// Update Prometheus metrics only if this is a valid user session.
				if isValidUser(session.User) {
					labels := []string{info.DisplayName, session.ExeName, info.Category, session.User, hostname}
					m.AppUsageSeconds.WithLabelValues(labels...).Add(session.CheckpointDelta.Seconds())
					m.AppForegroundSeconds.WithLabelValues(labels...).Add(session.ForegroundDelta.Seconds())
					m.AppLaunches.WithLabelValues(labels...).Inc()
				}

				// Update user session tracking.
				if isValidUser(session.User) {
					userSessions.onProcessStop(session.User, session.PID)
				}

				// Persist to local store.
				if err := db.RecordSession(
					session.PID, session.ExeName, session.ExePath,
					info.DisplayName, info.Category, info.Publisher,
					session.User, hostname,
					session.StartTime, session.StopTime, session.ForegroundDelta.Seconds(),
				); err != nil {
					logger.Error("failed to record session", "error", err)
				}
			},
		})
		if err != nil {
			return fmt.Errorf("failed to create WMI watcher: %w", err)
		}

		// Start WMI watcher in background.
		go func() {
			if err := watcher.Run(ctx); err != nil {
				logger.Error("WMI watcher failed", "error", err)
			}
		}()

		// Start periodic checkpoint loop for active process groups.
		go runCheckpointLoop(ctx, tracker, norm, m, cfg.Monitor.ReconcileInterval, logger)

		// Start foreground window poller
		go monitor.RunForegroundPoller(ctx, tracker, 1*time.Second, logger)

		// Start inventory scanner in background.
		invScanner := inventory.NewScanner(logger)
		go runInventoryLoop(ctx, invScanner, m, cfg.Inventory.ScanInterval, logger)

		// Start mapping file refresh in background.
		if mapping != nil && cfg.Normalizer.MappingRefreshInterval > 0 {
			go runMappingRefreshLoop(ctx, mapping, norm, cfg.Normalizer.MappingRefreshInterval, logger)
		}

		// Start enrollment heartbeat if a central server is configured.
		if cfg.Server.ReportURL != "" {
			enrollClient := enrollment.NewClient(cfg.Server.ReportURL, cfg.Server.Port, logger)
			go enrollClient.RunHeartbeat(ctx, 2*time.Minute)
		}

		// Set device info metric.
		setDeviceInfo(m)

		// Start Prometheus HTTP server.
		mux := http.NewServeMux()
		mux.Handle(cfg.Server.MetricsPath, promhttp.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})

		addr := fmt.Sprintf(":%d", cfg.Server.Port)
		srv := &http.Server{Addr: addr, Handler: mux}

		go func() {
			logger.Info("metrics server starting", "addr", addr, "path", cfg.Server.MetricsPath)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("metrics server failed", "error", err)
			}
		}()

		// Handle console mode Ctrl+C.
		sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		<-sigCtx.Done()
		logger.Info("shutting down...")

		// Graceful shutdown of HTTP server.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)

		return nil
	}
}

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

				// For total usage, we only want to count the checkpoint interval ONCE per unique app/user/host.
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

func setDeviceInfo(m *metrics.Metrics) {
	hostname := metrics.Hostname()

	// Read OS version from registry.
	osVersion := "unknown"
	osBuild := "unknown"

	// These are best-effort reads.
	// In production, we'd use proper Windows API calls.
	m.DeviceInfo.WithLabelValues(hostname, osVersion, osBuild, "").Set(1)
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
	// This helps catch cases where process names might leak into owner queries.
	for _, suffix := range []string{".exe", ".dll", ".sys", ".com", ".msc", ".scr", ".bat", ".cmd"} {
		if strings.HasSuffix(lower, suffix) {
			return false
		}
	}

	// 5. Minimal length check (human usernames are usually at least 2 chars).
	if len(user) < 2 {
		return false
	}

	return true
}

// userSessionManager derives user login/logoff events from process start/stop events.
// When a user's first tracked process appears, it counts as a login.
// When a user's last tracked process ends, it counts as a logoff.
type userSessionManager struct {
	mu         sync.Mutex
	users      map[string]*userState // user -> state
	metrics    *metrics.Metrics
	logger     *slog.Logger
}

type userState struct {
	activePIDs map[uint32]bool
	loginTime  time.Time
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
		state = &userState{
			activePIDs: make(map[uint32]bool),
			loginTime:  time.Now(),
		}
		usm.users[user] = state

		usm.metrics.UserSessionLogins.WithLabelValues(user, hostname).Inc()
		usm.metrics.UserSessionActive.WithLabelValues(user, hostname).Set(1)
		usm.logger.Info("user session started", "user", user)
	}

	state.activePIDs[pid] = true
}

func (usm *userSessionManager) onProcessStop(user string, pid uint32) {
	usm.mu.Lock()
	defer usm.mu.Unlock()

	hostname := metrics.Hostname()

	state, exists := usm.users[user]
	if !exists {
		return
	}

	delete(state.activePIDs, pid)

	if len(state.activePIDs) == 0 {
		// Last process ended — counts as a logoff.
		duration := time.Since(state.loginTime).Seconds()
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
