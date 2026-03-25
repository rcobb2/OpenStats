//go:build windows

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
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

// runAgent returns a function that runs the full agent lifecycle on Windows.
func runAgent(cfg *config.Config, logger *slog.Logger) service.AgentRunner {
	return func(ctx context.Context) error {
		logger.Info("starting OpenLabStats agent", "version", "0.1.5")

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

		// Scan for existing processes that started before the agent.
		logger.Info("scanning for existing processes...")
		existingProcs := monitor.ScanExistingProcesses(logger, norm.ResolveFamily)

		// Initialize user session manager before the existing process loop so
		// pre-existing processes are reflected in the session refcount at startup.
		userSessions := newUserSessionManager(m, logger)

		for _, p := range existingProcs {
			tracker.RegisterExistingProcess(p.PID, p.ParentPID, p.ExeName, p.ExePath, p.User, p.FamilyKey)
			if isValidUser(p.User) {
				userSessions.onProcessStart(p.User, p.PID)
			}
		}

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
				WithOSVersion(getWindowsOSCaption(logger))
			go enrollClient.RunHeartbeat(ctx, 2*time.Minute)
		}

		// Set device info metric.
		setDeviceInfo(m, logger)

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

func setDeviceInfo(m *metrics.Metrics, logger *slog.Logger) {
	hostname := metrics.Hostname()

	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		oleerr, ok := err.(*ole.OleError)
		if !ok || oleerr.Code() != 0x00000001 { // S_FALSE
			logger.Warn("COM init for device info failed", "error", err)
		}
	} else {
		defer ole.CoUninitialize()
	}

	model, manufacturer := getWMIProps(logger, "Win32_ComputerSystem", "Model", "Manufacturer")
	serial, _ := getWMIProps(logger, "Win32_BIOS", "SerialNumber", "")
	osCaption, osBuild := getWMIProps(logger, "Win32_OperatingSystem", "Caption", "BuildNumber")

	if model == "" {
		model = "unknown"
	}
	if manufacturer == "" {
		manufacturer = "unknown"
	}
	if serial == "" {
		serial = "unknown"
	}

	m.DeviceInfo.WithLabelValues(hostname, osCaption, osBuild, "", model, manufacturer, serial).Set(1)
}

// getWindowsOSCaption returns the Windows product name (e.g. "Microsoft Windows 11 Pro")
// from WMI. Used so the server can identify the agent as Windows and serve the correct
// .msi installer URL. Falls back to "Windows" on error.
func getWindowsOSCaption(logger *slog.Logger) string {
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		oleerr, ok := err.(*ole.OleError)
		if !ok || oleerr.Code() != 0x00000001 { // S_FALSE — already initialized
			return "Windows"
		}
	} else {
		defer ole.CoUninitialize()
	}
	caption, _ := getWMIProps(logger, "Win32_OperatingSystem", "Caption", "")
	if caption == "" {
		return "Windows"
	}
	return caption
}

func getWMIProps(logger *slog.Logger, class string, prop1, prop2 string) (string, string) {
	locator, err := oleutil.CreateObject("WbemScripting.SWbemLocator")
	if err != nil {
		return "", ""
	}
	defer locator.Release()

	wmi, err := locator.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return "", ""
	}
	defer wmi.Release()

	serviceRaw, err := oleutil.CallMethod(wmi, "ConnectServer")
	if err != nil {
		return "", ""
	}
	svc := serviceRaw.ToIDispatch()
	defer svc.Release()

	query := "SELECT * FROM " + class
	resultRaw, err := oleutil.CallMethod(svc, "ExecQuery", query)
	if err != nil {
		return "", ""
	}
	result := resultRaw.ToIDispatch()
	defer result.Release()

	countVar, err := oleutil.GetProperty(result, "Count")
	if err != nil || countVar.Val == 0 {
		return "", ""
	}

	itemRaw, err := oleutil.CallMethod(result, "ItemIndex", 0)
	if err != nil {
		return "", ""
	}
	item := itemRaw.ToIDispatch()
	defer item.Release()

	v1 := ""
	if prop1 != "" {
		if val, err := oleutil.GetProperty(item, prop1); err == nil {
			v1 = val.ToString()
		}
	}

	v2 := ""
	if prop2 != "" {
		if val, err := oleutil.GetProperty(item, prop2); err == nil {
			v2 = val.ToString()
		}
	}

	return v1, v2
}
