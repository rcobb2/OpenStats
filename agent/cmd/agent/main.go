package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/rcobb/openlabstats-agent/internal/config"
	"github.com/rcobb/openlabstats-agent/internal/enrollment"
	"github.com/rcobb/openlabstats-agent/internal/service"
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
			fmt.Println("openlabstats-agent v" + enrollment.AgentVersion)
			return

		case "serveraddress":
			handleServerAddress()
			return

		case "building":
			handleBuilding()
			return

		case "room":
			handleRoom()
			return

		case "heartbeat":
			handleHeartbeat()
			return

		case "maintenancewindow":
			handleMaintenanceWindow()
			return

		case "setmaintenance":
			handleSetMaintenance()
			return

		case "status":
			handleStatus()
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

func handleServerAddress() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(cfg.Server.ReportURL)
}

func handleBuilding() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(cfg.Monitor.Building)
}

func handleRoom() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(cfg.Monitor.Room)
}

func handleHeartbeat() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	client := enrollment.NewClient(cfg.Server.ReportURL, cfg.Server.Port, cfg.Monitor.Building, cfg.Monitor.Room, slog.Default())
	settings, err := client.GetSettings(context.Background())
	if err != nil {
		fmt.Printf("Configured: %ds (server unreachable, showing default)\n", 120)
		fmt.Printf("Actual: unknown (server unreachable)\n")
		return
	}

	fmt.Printf("Configured: %ds\n", settings.HeartbeatIntervalSeconds)
	fmt.Printf("Actual: %ds\n", settings.HeartbeatIntervalSeconds)
}

func handleMaintenanceWindow() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	override := enrollment.ReadMaintenanceOverride(cfg.BaseDir)

	client := enrollment.NewClient(cfg.Server.ReportURL, cfg.Server.Port, cfg.Monitor.Building, cfg.Monitor.Room, slog.Default())
	settings, err := client.GetSettings(context.Background())
	if err != nil {
		fmt.Printf("In Maintenance Window: unknown (server unreachable)\n")
		fmt.Printf("Configured Window: %s - %s\n", "22:00", "04:00")
		fmt.Printf("Override: %s\n", maintenanceOverrideStatus(override))
		return
	}

	inWindow := enrollment.IsInMaintenanceWindow(settings.MaintenanceWindowStart, settings.MaintenanceWindowEnd)

	if override != nil {
		fmt.Printf("In Maintenance Window: %v (override)\n", *override)
	} else {
		fmt.Printf("In Maintenance Window: %v\n", inWindow)
	}
	fmt.Printf("Configured Window: %s - %s\n", settings.MaintenanceWindowStart, settings.MaintenanceWindowEnd)
	fmt.Printf("Override: %s\n", maintenanceOverrideStatus(override))
}

func handleSetMaintenance() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: openlabstats-agent setmaintenance <true|false>\n")
		os.Exit(1)
	}

	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Previously set an in-process variable and exited — every CLI
	// invocation is its own process, so it never persisted, and the running
	// agent service never consulted it either: this command printed a
	// confident "forced on/off" while changing nothing an admin trying to
	// block an update during, say, a maintenance blackout could rely on.
	// WriteMaintenanceOverride persists a marker file the running service's
	// own self-update gating (Client.inMaintenanceWindow) now reads.
	var writeErr error
	arg := strings.ToLower(os.Args[2])
	switch arg {
	case "true", "1", "yes", "on":
		val := true
		writeErr = enrollment.WriteMaintenanceOverride(cfg.BaseDir, &val)
		fmt.Println("Maintenance mode: forced ON")
	case "false", "0", "no", "off":
		val := false
		writeErr = enrollment.WriteMaintenanceOverride(cfg.BaseDir, &val)
		fmt.Println("Maintenance mode: forced OFF")
	case "auto", "clear":
		writeErr = enrollment.WriteMaintenanceOverride(cfg.BaseDir, nil)
		fmt.Println("Maintenance mode: auto (time-based)")
	default:
		fmt.Fprintf(os.Stderr, "Invalid value: %s (use true, false, or auto)\n", os.Args[2])
		os.Exit(1)
	}
	if writeErr != nil {
		fmt.Fprintf(os.Stderr, "Failed to persist maintenance override: %v\n", writeErr)
		os.Exit(1)
	}
}

func maintenanceOverrideStatus(override *bool) string {
	if override == nil {
		return "auto"
	}
	if *override {
		return "forced on"
	}
	return "forced off"
}

func handleStatus() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Version:      %s\n", enrollment.AgentVersion)
	fmt.Printf("Building:     %s\n", cfg.Monitor.Building)
	fmt.Printf("Room:         %s\n", cfg.Monitor.Room)
	fmt.Printf("Server:       %s\n", cfg.Server.ReportURL)
	fmt.Printf("Metrics Port: %d\n", cfg.Server.Port)

	client := enrollment.NewClient(cfg.Server.ReportURL, cfg.Server.Port, cfg.Monitor.Building, cfg.Monitor.Room, slog.Default())
	settings, err := client.GetSettings(context.Background())
	if err != nil {
		fmt.Printf("Server:       unreachable\n")
		fmt.Printf("Heartbeat:    unknown (server unreachable)\n")
		fmt.Printf("Maintenance:  unknown (server unreachable)\n")
		return
	}

	inWindow := enrollment.IsInMaintenanceWindow(settings.MaintenanceWindowStart, settings.MaintenanceWindowEnd)
	maintStatus := fmt.Sprintf("%v", inWindow)
	if override := enrollment.ReadMaintenanceOverride(cfg.BaseDir); override != nil {
		maintStatus = fmt.Sprintf("%v (override)", *override)
	}

	fmt.Printf("Server:       connected\n")
	fmt.Printf("Heartbeat:    %ds\n", settings.HeartbeatIntervalSeconds)
	fmt.Printf("Maintenance:  %s\n", maintStatus)
}

func printUsage() {
	fmt.Println(`OpenLabStats Agent - Software usage tracking for higher education

Usage:
  openlabstats-agent [command] [options]

Commands:
  install              Install as a system service
  uninstall            Remove the system service
  version              Print version information
  serveraddress        Print configured server URL
  building             Print configured building
  room                 Print configured room
  heartbeat            Print heartbeat interval (from server)
  maintenancewindow    Print maintenance window status
  setmaintenance <val> Set maintenance override (true/false/auto)
  status               Print full agent status
  help                 Show this help message

Options:
  --config <path>      Path to configuration file (default: configs/agent.yaml)

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
