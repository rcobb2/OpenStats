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
		logger.Info("starting OpenLabStats agent", "version", enrollment.AgentVersion)

		// Initialize metrics.
		m := metrics.New()

		// Create the enrollment client first and pull the user policy before any
		// metrics are restored or processes are scanned, so nothing is ever
		// recorded under an ignored or non-canonical username.
		var enrollClient *enrollment.Client
		if cfg.Server.ReportURL != "" {
			enrollClient = enrollment.NewClient(cfg.Server.ReportURL, cfg.Server.Port, cfg.Monitor.Building, cfg.Monitor.Room, logger).
				WithOSVersion(getWindowsOSCaption(logger)).
				WithUserPolicyHandler(func(p *enrollment.UserPolicy) { applyUserPolicy(p, logger) })
			bootstrapUserPolicy(ctx, enrollClient, logger)
		} else {
			// Without a server URL the agent still collects metrics locally but
			// never registers and never self-updates. That used to be entirely
			// silent — e.g. an MSI installed by double-click rather than with
			// SERVERADDRESS set — so say so loudly.
			logger.Warn("no server.reportURL configured: agent will NOT register with a central server, " +
				"send heartbeats, or self-update; reinstall with SERVERADDRESS=<url> or set server.reportURL in agent.yaml")
		}

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

		for _, p := range existingProcs {
			tracker.RegisterExistingProcess(p.PID, p.ParentPID, p.ExeName, p.ExePath, p.User, p.FamilyKey)
		}

		// Set up WMI watcher.
		watcher, err := monitor.NewWMIWatcher(tracker, logger, monitor.WMIWatcherConfig{
			ExcludePatterns: cfg.Monitor.ExcludePatterns,
			MinLifetime:     cfg.Monitor.MinLifetime,
			FamilyResolver:  norm.ResolveFamily,
			OnElevated: func(pid uint32, exeName, exePath, rawUser string) {
				info := norm.Resolve(exeName, exePath)
				if user, ok := resolveUser(rawUser); ok {
					m.PrivilegeElevations.WithLabelValues(info.DisplayName, exeName, info.Category, user, metrics.Hostname()).Inc()
				}
				// Persist the raw user (like RecordSession); canonicalization
				// happens on restore.
				if err := db.RecordElevation(exeName, info.DisplayName, info.Category, rawUser, metrics.Hostname()); err != nil {
					logger.Error("failed to record elevation", "error", err)
				}
				logger.Info("privilege elevation detected", "pid", pid, "exe", exeName, "user", rawUser)
			},
			OnStop: func(session *monitor.ProcessSession) {
				// Resolve the friendly name and record the session.
				info := norm.Resolve(session.ExeName, session.ExePath)
				hostname := metrics.Hostname()

				// Update Prometheus metrics only if this is a valid user session.
				if user, ok := resolveUser(session.User); ok {
					labels := []string{info.DisplayName, session.ExeName, info.Category, user, hostname}
					m.AppUsageSeconds.WithLabelValues(labels...).Add(session.CheckpointDelta.Seconds())
					m.AppForegroundSeconds.WithLabelValues(labels...).Add(session.ForegroundDelta.Seconds())
					m.AppLaunches.WithLabelValues(labels...).Inc()
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

		// Start the OS logon session poller. User sessions come from the OS
		// (WTS/utmpx), not from process refcounts; 15s keeps sign-in and sign-out
		// edges tight without polling the session APIs hard.
		go newLogonTracker(m, logger).Run(ctx, 15*time.Second)

		// Start foreground window poller.
		go monitor.RunForegroundPoller(ctx, tracker, 1*time.Second, logger)

		// Start inventory scanner in background.
		invScanner := inventory.NewScanner(logger)
		go runInventoryLoop(ctx, invScanner, m, cfg.Inventory.ScanInterval, logger)

		// Start mapping file refresh in background.
		if mapping != nil && cfg.Normalizer.MappingRefreshInterval > 0 {
			go runMappingRefreshLoop(ctx, mapping, norm, cfg.Normalizer.MappingRefreshInterval, logger)
		}

		// Start enrollment heartbeat and metrics push if a central server is configured.
		// Each heartbeat also refreshes the user policy via the handler above.
		if enrollClient != nil {
			go enrollClient.RunHeartbeat(ctx, 2*time.Minute)
			go enrollClient.RunMetricsPush(ctx, 30*time.Second)
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
