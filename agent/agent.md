# Agent Component Documentation

The OpenLabStats Windows agent runs as a service on lab computers and tracks software usage through WMI event subscriptions and process monitoring.

## Overview

The agent:
1. Subscribes to WMI process start/stop events
2. Tracks foreground window changes
3. Normalizes software names (PE metadata + mapping file)
4. Filters out system/service accounts (`SYSTEM`, `DWM`, `NT SERVICE`, etc.)
5. Exposes Prometheus metrics for scraping
6. Registers with central server for fleet management

## Key Packages

### `internal/metrics/prometheus.go`

Defines all Prometheus metrics exposed by the agent:

| Metric | Type | Labels |
|--------|------|--------|
| `openlabstats_app_usage_seconds_total` | Counter | app, exe, category, user, hostname |
| `openlabstats_app_foreground_seconds_total` | Counter | app, exe, category, user, hostname |
| `openlabstats_app_launches_total` | Counter | app, exe, category, user, hostname |
| `openlabstats_app_active` | Gauge | app, exe, category, user, hostname |
| `openlabstats_user_session_active` | Gauge | user, hostname |
| `openlabstats_user_session_duration_seconds` | Gauge | user, hostname |
| `openlabstats_user_session_logins_total` | Counter | user, hostname |
| `openlabstats_user_session_seconds_total` | Counter | user, hostname |
| `openlabstats_device_info` | Gauge | hostname, os_version, os_build, domain |
| `openlabstats_installed_software_info` | Gauge | name, version, publisher, hostname |

### `internal/monitor/wmi.go`

WMI event subscription for process tracking:
- Subscribes to `Win32_ProcessStartTrace` and `Win32_ProcessStopTrace`
- Filters processes using `excludePatterns` config
- Tracks process user via `Win32_Process` and token lookup
- Emits events via callbacks: `OnStart`, `OnStop`

### `internal/monitor/tracker.go`

Active process state management:
- Maintains map of running processes
- Handles checkpointing for metrics aggregation
- Deduplicates by (app, user, hostname) to prevent double-counting
- **User Filtering**: Only records metrics for valid human users (excludes system/computer accounts)
- Integrates with foreground poller

### `internal/monitor/foreground.go`

Foreground window polling:
- Polls `GetForegroundWindow` Win32 API
- Maps window handle to process
- Reports foreground delta per process group

### `internal/normalizer/`

Software name resolution:
- `mapping.go` - Reads `software-map.json`, provides lookup by exe name
- `normalizer.go` - Combines mapping + PE metadata
- `pe.go` - Reads PE version info (FileDescription, CompanyName)

### `internal/inventory/registry.go`

Installed software scanning:
- Reads `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
- Also `HKLM\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`
- Populates `openlabstats_installed_software_info` gauge

### `internal/enrollment/client.go`

Server communication:
- `Register()` - POST to `/api/v1/agents/register`
- `RunHeartbeat()` - Periodic registration (default 2 min)
- Sends: hostname, IP, OS version, agent version, port

### `internal/store/sqlite.go`

Local persistence:
- Stores completed sessions for metric restoration on restart
- Schema: sessions table with app, user, timestamps, foreground time

## Configuration (`configs/agent.yaml`)

```yaml
server:
  port: 9183              # Prometheus metrics port
  metricsPath: /metrics
  reportURL: ""           # Central server URL for enrollment

monitor:
  reconcileInterval: 30s  # Checkpoint interval
  minLifetime: 2s         # Ignore processes shorter than this
  excludePatterns:        # Regex patterns to skip

normalizer:
  mappingFile: configs/software-map.json
  mappingUpdateURL: ""    # Server endpoint for mapping updates
  mappingRefreshInterval: 1h

inventory:
  scanInterval: 1h

store:
  dbPath: data/openlabstats.db

logging:
  level: info
  filePath: logs/agent.log
```

## Building

```powershell
cd agent
go build -o openlabstats-agent.exe ./cmd/agent/
```

## Running

```powershell
# Console mode (for testing)
.\openlabstats-agent.exe --config configs\agent.yaml

# Install as Windows service
.\openlabstats-agent.exe install
net start OpenLabStats

# Uninstall
net stop OpenLabStats
.\openlabstats-agent.exe uninstall
```

## CLI Tools

The agent provides several CLI commands for querying status and configuration:

| Command | Description |
|---------|-------------|
| `--version` | Print agent version |
| `--serveraddress` | Print configured server URL |
| `--building` | Print configured building |
| `--room` | Print configured room |
| `--heartbeat` | Print heartbeat interval (from server settings) |
| `--maintenancewindow` | Print maintenance window status and configured times |
| `--setmaintenance <val>` | Set maintenance override (`true`, `false`, or `auto`) |
| `--status` | Print full agent status (version, building, room, server, heartbeat, maintenance) |

### Examples

```powershell
# Check agent version
.\openlabstats-agent.exe version

# Check server address
.\openlabstats-agent.exe serveraddress

# Check full status
.\openlabstats-agent.exe status

# Check maintenance window status
.\openlabstats-agent.exe maintenancewindow

# Force maintenance mode (useful before updates)
.\openlabstats-agent.exe setmaintenance true
```

### Offline Behavior

Commands that require server connection (`--heartbeat`, `--maintenancewindow`, `--status`) will show "unknown" or cached values if the server is unreachable. Config-based commands (`--version`, `--serveraddress`, `--building`, `--room`) work offline.

## MSI Installer

The agent can be deployed via MSI with full support for silent installation and remote configuration.

### Public Properties

| Property | Description | Default |
|----------|-------------|---------|
| `SERVERADDRESS` | URL of the central management server | `""` |
| `PORT` | Prometheus metrics scrape port | `9183` |
| `BUILDING` | Lab building name for auto-assignment | `""` |
| `ROOM` | Lab room number for auto-assignment | `""` |
| `INSTALLDIR` | Custom installation path | `C:\Program Files\OpenLabStats` |

### Silent Install Examples

```powershell
# Standard install with server enrollment
msiexec /i openlabstats-agent.msi /qn SERVERADDRESS="http://openlabstats.campus.edu:8080"

# Install with automatic lab and room assignment
msiexec /i openlabstats-agent.msi /qn SERVERADDRESS="http://server:8080" BUILDING="Science Hall" ROOM="302"
```

### Auto-Registration Logic

When an agent registers with the server:
1. It sends the `BUILDING` and `ROOM` configured during installation.
2. The server looks for a lab matching that building and room.
3. If a match is found, the agent is automatically assigned to that lab.
4. If no match is found, the server **automatically creates a new lab** and assigns the agent to it.
```

## Metrics Endpoint

- **Metrics**: `http://localhost:9183/metrics`
- **Health**: `http://localhost:9183/health`

## Dependencies

- Go 1.21+
- Windows 10/11
- Prometheus (for collection)
- Optional: Central server (for fleet management)

## Common Tasks

### Adding a New Metric

1. Add to `internal/metrics/prometheus.go`
2. Wire up collection in `cmd/agent/main.go`
3. Update Grafana dashboards
4. Document in README.md

### Adding Exclude Pattern

Add regex to `monitor.excludePatterns` in `configs/agent.yaml`

### Modifying Software Mapping

Edit `configs/software-map.json` or manage via server UI
