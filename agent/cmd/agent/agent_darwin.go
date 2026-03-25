//go:build darwin

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
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

// runAgent returns a function that runs the full agent lifecycle on macOS.
func runAgent(cfg *config.Config, logger *slog.Logger) service.AgentRunner {
	return func(ctx context.Context) error {
		logger.Info("starting OpenLabStats agent (macOS)", "version", "0.1.5")

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
		plist := normalizer.NewPlistReader(logger)
		norm := normalizer.NewNormalizer(mapping, plist, logger)

		// Initialize process tracker.
		tracker := monitor.NewTracker(logger)

		// Initialize user session manager (derives login/logoff from process events).
		userSessions := newUserSessionManager(m, logger)

		// Set up macOS process watcher first so its FilterProcesses can be used
		// to apply the same exclusion rules to the startup scan below.
		watcher, err := monitor.NewPollWatcher(tracker, logger, monitor.WMIWatcherConfig{
			ExcludePatterns: cfg.Monitor.ExcludePatterns,
			MinLifetime:     cfg.Monitor.MinLifetime,
			FamilyResolver:  norm.ResolveFamily,
			OnStart: func(pid uint32, exeName string, isNewGroup bool) {
				if !isNewGroup {
					return
				}
				user := tracker.GetProcessUser(pid)
				if isValidUser(user) {
					userSessions.onProcessStart(user, pid)
				}
			},
			OnStop: func(session *monitor.ProcessSession) {
				info := norm.Resolve(session.ExeName, session.ExePath)
				hostname := metrics.Hostname()

				if isValidUser(session.User) {
					labels := []string{info.DisplayName, session.ExeName, info.Category, session.User, hostname}
					m.AppUsageSeconds.WithLabelValues(labels...).Add(session.CheckpointDelta.Seconds())
					m.AppForegroundSeconds.WithLabelValues(labels...).Add(session.ForegroundDelta.Seconds())
					m.AppLaunches.WithLabelValues(labels...).Inc()
				}

				if isValidUser(session.User) {
					userSessions.onProcessStop(session.User, session.PID)
				}

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
			return fmt.Errorf("failed to create process watcher: %w", err)
		}

		// Scan for existing processes that started before the agent and register
		// them with the tracker, applying the same exclusion rules as the watcher.
		logger.Info("scanning for existing processes...")
		existingProcs := monitor.ScanExistingProcesses(logger, norm.ResolveFamily)
		existingProcs = watcher.FilterProcesses(existingProcs)
		for _, p := range existingProcs {
			tracker.RegisterExistingProcess(p.PID, p.ParentPID, p.ExeName, p.ExePath, p.User, p.FamilyKey)
			if isValidUser(p.User) {
				userSessions.onProcessStart(p.User, p.PID)
			}
		}

		// Start process watcher in background.
		go func() {
			if err := watcher.Run(ctx); err != nil {
				logger.Error("process watcher failed", "error", err)
			}
		}()

		// Start periodic checkpoint loop for active process groups.
		go runCheckpointLoop(ctx, tracker, norm, m, userSessions, cfg.Monitor.ReconcileInterval, logger)

		// Start foreground window poller.
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
			enrollClient := enrollment.NewClient(cfg.Server.ReportURL, cfg.Server.Port, cfg.Monitor.Building, cfg.Monitor.Room, logger).
				WithOSVersion("macOS " + getOSVersionDarwin())
			go enrollClient.RunHeartbeat(ctx, 2*time.Minute)
		}

		// Set device info metric (macOS).
		setDeviceInfoDarwin(m, logger)

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

		// ctx is already cancelled by signal handler in service.Run (darwin).
		<-ctx.Done()
		logger.Info("shutting down...")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)

		return nil
	}
}

// setDeviceInfoDarwin sets the device_info metric with macOS hardware details
// obtained from sysctl.
func setDeviceInfoDarwin(m *metrics.Metrics, logger *slog.Logger) {
	hostname := metrics.Hostname()
	osVersion := getOSVersionDarwin()
	osBuild, _ := syscall.Sysctl("kern.osversion")
	model, _ := syscall.Sysctl("hw.model")
	if model == "" {
		model = "unknown"
	}

	m.DeviceInfo.WithLabelValues(hostname, "macOS "+osVersion, osBuild, "", model, "Apple Inc.", "unknown").Set(1)
}

// getOSVersionDarwin returns the macOS product version string (e.g. "14.3.1").
func getOSVersionDarwin() string {
	v, err := syscall.Sysctl("kern.osproductversion")
	if err != nil {
		return ""
	}
	return v
}
