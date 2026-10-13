//go:build darwin

package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/template"

	"github.com/rcobb/openlabstats-agent/internal/logrotate"
)

const ServiceLabel = "com.openlabstats.agent"
const plistPath = "/Library/LaunchDaemons/com.openlabstats.agent.plist"

// stdoutLogPath/stderrLogPath are what the launchd plist's
// StandardOutPath/StandardErrorPath point at. launchd itself opens and
// appends to these — our own logger no longer echoes to stdout outside a
// real terminal (see cmd/agent's setupLogger), so in normal operation these
// should now stay small (crash output only). maxRedirectLogBytes bounds
// them anyway: truncateRedirectedLogs runs on every start, so a file that
// somehow grew past the cap self-heals on the next restart instead of
// accumulating forever, the same failure mode that let agent.log itself
// reach 100+ GB before rotation existed.
const (
	stdoutLogPath       = "/var/log/openlabstats/agent-stdout.log"
	stderrLogPath       = "/var/log/openlabstats/agent-stderr.log"
	maxRedirectLogBytes = 50 * 1024 * 1024
)

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.openlabstats.agent</string>

    <key>ProgramArguments</key>
    <array>
        <string>{{.ExePath}}</string>
        <string>--config</string>
        <string>{{.ConfigPath}}</string>
    </array>

    <key>RunAtLoad</key>
    <true/>

    <key>KeepAlive</key>
    <true/>

    <key>ThrottleInterval</key>
    <integer>30</integer>

    <key>StandardOutPath</key>
    <string>{{.StdoutLogPath}}</string>

    <key>StandardErrorPath</key>
    <string>{{.StderrLogPath}}</string>

    <key>WorkingDirectory</key>
    <string>{{.WorkDir}}</string>
</dict>
</plist>
`

// Run starts the agent directly. On macOS, launchd manages the service
// lifecycle externally — the binary just runs until SIGTERM/SIGINT.
func Run(runner AgentRunner, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	truncateRedirectedLogs(logger)

	logger.Info("running as macOS process (launchd manages service lifecycle)")
	return runner(ctx)
}

// truncateRedirectedLogs reclaims space from launchd's stdout/stderr
// redirect files if either has already grown past the cap — most relevantly
// on upgrade from an agent version that still echoed debug logging to
// stdout unconditionally (see cmd/agent's setupLogger), which duplicated
// everything into agent-stdout.log with no cap of its own.
func truncateRedirectedLogs(logger *slog.Logger) {
	for _, path := range []string{stdoutLogPath, stderrLogPath} {
		truncated, err := logrotate.TruncateIfOversized(path, maxRedirectLogBytes)
		if err != nil {
			logger.Warn("failed to check launchd-redirected log size", "path", path, "error", err)
			continue
		}
		if truncated {
			logger.Info("truncated oversized launchd-redirected log", "path", path)
		}
	}
}

// Install writes the launchd plist to /Library/LaunchDaemons/ and loads it.
func Install(exePath string) error {
	// Ensure log directory exists.
	if err := os.MkdirAll(filepath.Dir(stdoutLogPath), 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Determine config path relative to the executable.
	exeDir := filepath.Dir(exePath)
	configPath := filepath.Join(exeDir, "configs", "agent.yaml")

	tmpl, err := template.New("plist").Parse(plistTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse plist template: %w", err)
	}

	f, err := os.Create(plistPath)
	if err != nil {
		return fmt.Errorf("failed to create plist at %s: %w", plistPath, err)
	}
	defer f.Close()

	data := struct {
		ExePath       string
		ConfigPath    string
		WorkDir       string
		StdoutLogPath string
		StderrLogPath string
	}{
		ExePath:       exePath,
		ConfigPath:    configPath,
		WorkDir:       exeDir,
		StdoutLogPath: stdoutLogPath,
		StderrLogPath: stderrLogPath,
	}

	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("failed to write plist: %w", err)
	}
	f.Close()

	// Set correct ownership and permissions.
	if err := os.Chmod(plistPath, 0644); err != nil {
		return fmt.Errorf("failed to set plist permissions: %w", err)
	}

	// Bootstrap the daemon with launchd.
	cmd := exec.Command("launchctl", "bootstrap", "system", plistPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap failed: %w\n%s", err, out)
	}

	return nil
}

// Uninstall stops and removes the launchd daemon.
func Uninstall() error {
	// Bootout the daemon.
	cmd := exec.Command("launchctl", "bootout", "system/"+ServiceLabel)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Not fatal — daemon may already be stopped.
		fmt.Fprintf(os.Stderr, "launchctl bootout warning: %v\n%s\n", err, out)
	}

	// Remove the plist file.
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove plist: %w", err)
	}

	return nil
}
