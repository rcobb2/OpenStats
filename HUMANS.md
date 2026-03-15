# OpenLabStats - Architecture Overview

OpenLabStats is an open-source software usage tracking solution for higher education labs. It provides real-time visibility into application usage, user sessions, and installed software across Windows machines.

## System Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   Windows       │     │   Central       │     │   Monitoring    │
│   Agent         │     │   Server        │     │   Stack         │
│                 │     │                 │     │                 │
│ - WMI monitor   │────▶│ - REST API      │◀────│ - Prometheus    │
│ - Metrics       │     │ - PostgreSQL    │     │ - Grafana       │
│ - SQLite local  │     │ - File SD       │     │                 │
└─────────────────┘     └─────────────────┘     └─────────────────┘
        │                       │
        │ HTTP /metrics         │ file_sd
        │                       │
        ▼                       ▼
┌─────────────────┐     ┌─────────────────┐
│ Prometheus      │     │ Prometheus      │
│ scrapes agents  │     │ watches targets │
└─────────────────┘     └─────────────────┘
```

## Components

### Agent (Windows)

The agent runs as a Windows service on lab computers and tracks:

- **Software usage** - Application launch/exit events via WMI
- **Foreground time** - Which app has the active window
- **User sessions** - Derived from process ownership
- **Installed software** - Registry scans

Data is exposed as Prometheus metrics at `http://<host>:9183/metrics`.

**Key packages:**
- `internal/monitor/` - WMI process tracking, foreground window polling
- `internal/metrics/` - Prometheus metric definitions
- `internal/normalizer/` - Software name resolution (PE metadata + mapping file)
- `internal/inventory/` - Registry-based installed software scanning
- `internal/enrollment/` - Server heartbeat registration
- `internal/store/` - SQLite local persistence

### Server (Go)

Central management server providing:

- **Agent registration** - Heartbeat endpoint, status tracking
- **Lab management** - Grouping agents by physical location
- **Software mappings** - CRUD for normalization rules
- **Reporting** - Aggregated usage data
- **Installer generation** - Customized agent installers
- **Prometheus discovery** - file_sd JSON for auto-targeting

**API:** RESTful at `/api/v1/*` (see Swagger docs at `/api/docs`)

**Database:** PostgreSQL with tables for agents, labs, mappings, installer builds.

### Frontend (React/Vite)

Web UI for managing the fleet:

- **Dashboard** - Overview metrics
- **Labs** - Lab/room management
- **Agents** - Fleet inventory, status
- **Mappings** - Software name normalization rules
- **Reports** - Usage analytics
- **Installer** - Generate customized agent packages

### Infrastructure

- **Prometheus** - Scrapes agents via file_sd (updated by server)
- **Grafana** - Dashboards for visualization
- **Docker Compose** - Full stack deployment

## Data Flow

1. **Agent starts** → Registers with server (`POST /api/v1/agents/register`)
2. **Server** → Adds agent to PostgreSQL, refreshes Prometheus targets
3. **Prometheus** → Scrapes `/metrics` from all registered agents
4. **Grafana** → Queries Prometheus for dashboards
5. **User logs in** → Agent derives session from first process
6. **App launched** → WMI event → Normalize name → Update metrics
7. **Server heartbeat** → Agent re-registers every 2 minutes

## Configuration

### Agent (`agent/configs/agent.yaml`)
- `server.port` - Prometheus metrics port (default 9183)
- `server.reportURL` - Central server for enrollment
- `monitor.excludePatterns` - Regex for processes to ignore
- `normalizer.mappingFile` - Software name mappings

### Server (`server/config/server.yaml`)
- `server.port` - API port (default 8080)
- `database.*` - PostgreSQL connection
- `fileSD.outputPath` - Where to write Prometheus targets

## Development

### Building

```powershell
# Agent
cd agent && go build -o openlabstats-agent.exe ./cmd/agent/

# Server
cd server && go build -o server.exe ./cmd/server/

# Frontend
cd server/web && npm install && npm run build
```

### Running Locally

```powershell
# Agent (console mode)
.\agent\openlabstats-agent.exe --config agent\configs\agent.yaml

# Server
.\server\server.exe server\config\server.yaml

# Full stack (Docker)
cd server && docker-compose up -d
```

## Metrics Reference

| Metric | Type | Description |
|--------|------|-------------|
| `openlabstats_app_usage_seconds_total` | Counter | Cumulative app runtime |
| `openlabstats_app_foreground_seconds_total` | Counter | Active window time |
| `openlabstats_app_launches_total` | Counter | Launch count |
| `openlabstats_user_session_seconds_total` | Counter | User login time |
| `openlabstats_installed_software_info` | Gauge | Installed software inventory |

## License

MIT
