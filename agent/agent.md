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
