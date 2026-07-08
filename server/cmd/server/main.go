package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rcobb/openlabstats-server/internal/api"
	"github.com/rcobb/openlabstats-server/internal/config"
	"github.com/rcobb/openlabstats-server/internal/discovery"
	"github.com/rcobb/openlabstats-server/internal/store"

	_ "github.com/rcobb/openlabstats-server/docs"
)

// @title        OpenLabStats Server API
// @version      0.1.3
// @description  Central management server for the OpenLabStats fleet. Manages agent enrollment, software mappings, lab groupings, and reporting.
// @host         localhost:8080
// @BasePath     /

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Determine config path.
	configPath := "config/server.yaml"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Connect to PostgreSQL.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	db, err := store.New(ctx, cfg.Database.DSN())
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	logger.Info("database connected")

	// Initialize Prometheus file_sd_configs writer.
	disc := discovery.NewFileSD(cfg.FileSD.OutputPath, logger)

	// Do an initial target refresh.
	if err := disc.Refresh(context.Background(), db); err != nil {
		logger.Warn("initial target refresh failed", "error", err)
	}

	// Root context cancelled on shutdown to stop background goroutines.
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// Start background stale-agent checker.
	go runStaleChecker(rootCtx, db, disc, logger)

	// Set up HTTP server.
	router := api.NewRouter(db, cfg, disc, logger)
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server; surface fatal errors through a channel so deferred
	// cleanup (db.Close) still runs instead of os.Exit bypassing it.
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Wait for shutdown signal or server error.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sigCh:
	case err := <-serverErr:
		logger.Error("server failed", "error", err)
	}

	logger.Info("shutting down...")
	rootCancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}
}

// runStaleChecker periodically marks agents that haven't checked in as offline
// and refreshes Prometheus targets. Stops when ctx is cancelled.
func runStaleChecker(ctx context.Context, db *store.Store, disc *discovery.FileSD, logger *slog.Logger) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		tickCtx, cancel := context.WithTimeout(ctx, 20*time.Second)

		settings, err := db.GetSettings(tickCtx)
		if err != nil {
			logger.Error("failed to get settings for stale check", "error", err)
			cancel()
			continue
		}

		// Mark as offline if no check-in for 2.5x heartbeat interval (min 5m).
		offlineThreshold := time.Duration(settings.HeartbeatIntervalSeconds*25/10) * time.Second
		if offlineThreshold < 5*time.Minute {
			offlineThreshold = 5 * time.Minute
		}

		if err := db.MarkStaleAgents(tickCtx, offlineThreshold); err != nil {
			logger.Error("failed to mark stale agents", "error", err)
		} else {
			if err := disc.Refresh(tickCtx, db); err != nil {
				logger.Error("failed to refresh targets after stale check", "error", err)
			}
		}

		if settings.StaleTimeoutDays > 0 {
			if err := db.DeleteStaleAgents(tickCtx, settings.StaleTimeoutDays); err != nil {
				logger.Error("failed to delete stale agents", "error", err)
			}
		}

		cancel()
	}
}
