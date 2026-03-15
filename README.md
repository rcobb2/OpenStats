# OpenLabStats Agent

Open-source software usage tracking for higher education. An alternative to proprietary solutions like LabStats.

## What It Does

The OpenLabStats agent runs as a Windows service on lab and classroom computers. It tracks:

- **Software usage** — which applications are launched, how long they run, and who uses them (human only)
- **Launch counts** — how many times each application is opened
- **Foreground tracking** — active vs background usage for precise analytics
- **User sessions** — when users log in and out
- **Software inventory** — what's installed on each machine

Data is exposed as Prometheus metrics, ready for scraping into your existing Prometheus + Grafana stack.

## Architecture

```
[Windows Agent] --HTTP /metrics--> [Prometheus] --> [Grafana Dashboards]
     |
     +-- WMI event subscriptions (process start/stop)
     +-- PE metadata extraction (software name resolution)
     +-- Server-managed mapping file (name normalization)
     +-- SQLite (local data persistence)
     +-- Registry scan (installed software inventory)
```

## Quick Start

### Prerequisites

- Go 1.21+ (for building from source)
- Windows 10/11
- Prometheus server (for metrics collection)
- Grafana (for dashboards)

### Build

```powershell
go build -o openlabstats-agent.exe ./cmd/agent/
```

### Run in Console Mode (for testing)

```powershell
.\openlabstats-agent.exe --config configs\agent.yaml
```

Then visit `http://localhost:9183/metrics` to see Prometheus metrics.

### Install as Windows Service

```powershell
# Run as Administrator
.\openlabstats-agent.exe install
net start OpenLabStats
```

### Uninstall

```powershell
net stop OpenLabStats
.\openlabstats-agent.exe uninstall
```

## Configuration

Edit `configs/agent.yaml`:

```yaml
server:
  port: 9183              # Prometheus metrics port

monitor:
  reconcileInterval: 30s  # How often to reconcile process state
  minLifetime: 2s         # Ignore processes shorter than this
  excludePatterns:        # Regex patterns for processes to ignore
    - "^svchost\\.exe$"

normalizer:
  mappingFile: configs/software-map.json  # Software name mappings
  mappingUpdateURL: ""                     # URL for server-pushed updates

inventory:
  scanInterval: 1h        # How often to scan installed software

store:
  dbPath: data/openlabstats.db  # Local SQLite database
```

## Software Name Normalization

Raw process names (e.g., `EXCEL.EXE`) are normalized into friendly names using two tiers:

1. **Server-managed mapping file** (`configs/software-map.json`) — community-curated and server-pushable
2. **PE metadata extraction** — reads FileDescription from the executable's version resource

The central server (future) can push AI-informed mappings down to agents.

## Prometheus Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `openlabstats_app_usage_seconds_total` | Counter | Cumulative running time per app/user |
| `openlabstats_app_launches_total` | Counter | Launch count per app/user |
| `openlabstats_app_active` | Gauge | Whether an app is currently running |
| `openlabstats_user_session_active` | Gauge | Whether a user session is active |
| `openlabstats_user_session_duration_seconds` | Gauge | Current session duration |
| `openlabstats_device_info` | Gauge | Device metadata labels |
| `openlabstats_installed_software_info` | Gauge | Installed software inventory |

## Grafana Dashboard

Import the starter dashboard from `deploy/grafana-dashboard.json`. It includes:

- Top applications by usage time and launch count
- Usage breakdown by category (pie chart)
- Active applications and user sessions
- Installed software count per device

## Prometheus Configuration

Add to your `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: 'openlabstats'
    scrape_interval: 30s
    static_configs:
      - targets: ['lab-pc-01:9183', 'lab-pc-02:9183']
        labels:
          lab: 'library-101'
```

See `deploy/prometheus-scrape.yaml` for a full example.

## Deployment

The agent compiles to a single `openlabstats-agent.exe` with no runtime dependencies. Deploy via:

- **SCCM/Intune** — push the exe + configs folder
- **GPO** — install as a service via startup script
- **Manual** — copy files and run `openlabstats-agent.exe install`

## Roadmap

- [x] Central management server (mapping push, reporting)
- [x] Foreground window tracking (active vs background usage)
- [ ] AI Normalizer (automated categorization)
- [ ] macOS agent (using NSWorkspace/launchd)
- [ ] MSI installer package
- [ ] Web-based application tracking (browser URL categorization)

## License

MIT
