# OpenLabStats - Agent Coordination Guide

This file contains guidelines for AI agents working on the OpenLabStats project. It ensures consistency when multiple agents work on different components.

## Project Structure

```
WindowsLabStats/
├── HUMANS.md              # Architecture overview (for humans)
├── AGENTS.md              # This file - coordination rules
├── README.md              # User-facing documentation
├── .gitignore
│
├── agent/                 # Windows agent (Go)
│   ├── agent.md           # Agent-specific context
│   ├── cmd/agent/         # Entry point
│   ├── configs/           # Configuration files
│   ├── internal/
│   │   ├── config/       # Config loading
│   │   ├── enrollment/   # Server communication
│   │   ├── inventory/    # Registry scanning
│   │   ├── metrics/      # Prometheus definitions
│   │   ├── monitor/      # WMI tracking, foreground
│   │   ├── normalizer/   # Software name resolution
│   │   ├── service/      # Windows service wrapper
│   │   └── store/        # SQLite persistence
│   ├── installer/        # WiX installer
│   └── data/             # Runtime data (SQLite)
│
├── server/               # Central server (Go)
│   ├── server.md         # Server-specific context
│   ├── cmd/server/       # Entry point
│   ├── config/           # Configuration
│   ├── internal/
│   │   ├── api/          # REST handlers
│   │   ├── config/       # Config loading
│   │   ├── discovery/    # Prometheus file_sd
│   │   └── store/        # PostgreSQL operations
│   ├── web/              # Frontend (React)
│   │   ├── web.md        # Frontend context
│   │   ├── src/
│   │   │   ├── api.js    # API client
│   │   │   ├── pages/    # Page components
│   │   │   └── components/
│   │   └── package.json
│   ├── grafana/          # Dashboards
│   ├── prometheus/       # Config
│   └── docker-compose.yaml
│
└── deploy/               # Deployment configs
    ├── prometheus-scrape.yaml
    └── grafana-dashboard.json
```

## Communication Contracts

### Agent → Server
- **Registration**: `POST http://server:8080/api/v1/agents/register`
- **Payload**: `{ id, hostname, ipAddress, osVersion, agentVersion, port, building, room }`
- **Heartbeat**: Every 2 minutes

### Server → Prometheus
- **Discovery file**: Written to path in `server.config.fileSD.outputPath`
- **Format**: JSON file_sd format with `targets`, `labels`

### Prometheus → Agent
- **Metrics endpoint**: `http://agent:9183/metrics`
- **Health check**: `http://agent:9183/health`

### Frontend → Server
- **Base**: `/api/v1/*`
- **All endpoints**: JSON, no auth (CORS open for dev)

## Change Checklists

When modifying the system, update ALL applicable components:

### Adding a New Metric (Agent)

- [ ] `agent/internal/metrics/prometheus.go` - Add metric definition
- [ ] `agent/cmd/agent/main.go` - Wire up initialization if needed
- [ ] `agent/cmd/agent/main.go` - Add CLI tool if new status query needed
- [ ] `agent/configs/agent.yaml` - Add config if applicable
- [ ] `agent/internal/enrollment/client.go` - If server API changed
- [ ] `README.md` - Update metrics table
- [ ] `server/grafana/dashboards/*.json` - Update dashboard
- [ ] `deploy/grafana-dashboard.json` - Update deploy copy
- [ ] `deploy/prometheus-scrape.yaml` - If scrape config needed

### Adding/Modifying Installer

- [ ] `agent/installer/Package.wxs` - WiX configuration
- [ ] `agent/installer/build.ps1` - Build script
- [ ] `agent/configs/agent.yaml` - Default values (server address, port)
- [ ] `server/docs/swagger.yaml` - If API changed for enrollment
- [ ] `README.md` - Update MSI install examples
- [ ] `agent/agent.md` - Update CLI tools section

### Adding CLI Flags

- [ ] `agent/cmd/agent/main.go` - Add CLI handler
- [ ] `README.md` - Update CLI Commands section
- [ ] `agent/agent.md` - Update CLI Tools section

### Adding a New API Endpoint

- [ ] `server/internal/api/router.go` - Add route
- [ ] `server/internal/api/<entity>.go` - Add handler
- [ ] `server/internal/store/postgres.go` - Add DB operations if needed
- [ ] `server/docs/swagger.yaml` - Run `swag init` to regenerate
- [ ] `server/web/src/api.js` - Add client function
- [ ] `server/web/src/pages/` - Add page component if needed
- [ ] `server/web/src/main.jsx` - Add route if needed
- [ ] `server/web/src/components/Layout.jsx` - Add nav link if needed

### Modifying Reports Page

- [ ] `server/web/src/pages/Reports.jsx` - Update page component
- [ ] `server/web/src/api.js` - Add API functions if new data needed
- [ ] Backend: Add/modify endpoints in `server/internal/api/reports.go`
- [ ] `server/grafana/dashboards/*.json` - Update Grafana panels if applicable
- [ ] `deploy/grafana-dashboard.json` - Sync deploy copy

### Adding a New Frontend Page

- [ ] `server/web/src/pages/<Name>.jsx` - Create page component
- [ ] `server/web/src/main.jsx` or router - Add route
- [ ] `server/web/src/components/Layout.jsx` - Add nav link
- [ ] `server/web/src/api.js` - Add API functions if data needed

### Adding/Modifying Grafana Dashboard

- [ ] `server/grafana/dashboards/*.json` - Update dashboard JSON
- [ ] `deploy/grafana-dashboard.json` - Sync deploy copy
- [ ] `server/prometheus/alerts.yml` - Add alerts if needed
- [ ] `server/prometheus/prometheus.yml` - Add rules if needed
- [ ] Frontend: Add link from Reports page if applicable

### Modifying Database Schema

- [ ] `server/internal/store/postgres.go` - Update migration in `migrate()`
- [ ] Check for existing data migration needs
- [ ] Update any affected API handlers
- [ ] Test with existing PostgreSQL instance

### Updating Documentation

Whenever a significant feature is added or architectural changes occur, update the corresponding documentation:

- [ ] `HUMANS.md` - Overall system architecture and data flow
- [ ] `README.md` - User-facing features and roadmap
- [ ] `agent/agent.md` - Agent-specific internal logic and metrics
- [ ] `server/server.md` - Server API, database schema, and components
- [ ] `server/web/web.md` - Frontend structure and components

## Best Practices

### Code Style
- Follow existing patterns in each component
- Use meaningful variable names
- Add comments for non-obvious logic
- Keep functions small and focused

### Error Handling
- Return descriptive errors (use `fmt.Errorf` with %w)
- Log errors with context
- Don't expose internal errors to clients

### Testing
- Test each component in isolation where possible
- For API: Test handler with mock store
- For agent: Unit test normalizer, monitor logic
- Integration: Run full stack with Docker Compose

### Configuration
- All settings in config files, not hardcoded
- Sensible defaults in config files
- Document new config options

### API Design
- RESTful patterns (GET/list, POST/create, PUT/update, DELETE/remove)
- JSON request/response bodies
- Proper HTTP status codes
- Add Swagger annotations for documentation

## Common Tasks

### Running Local Dev Environment

```powershell
# Full stack
cd server && docker-compose up -d

# Agent only (console mode)
cd agent && go run ./cmd/agent/ --config configs/agent.yaml

# Server only
cd server && go run ./cmd/server/ --config config/server.yaml

# Frontend dev server
cd server/web && npm run dev
```

### Running Tests

```powershell
# Agent tests
cd agent && go test ./...

# Server tests
cd server && go test ./...
```

### Building Release Binaries

```powershell
# Agent (Windows)
cd agent && go build -o openlabstats-agent.exe ./cmd/agent/

# Server
cd server && go build -o server.exe ./cmd/server/

# Frontend
cd server/web && npm run build
```

### Building Installer

```powershell
cd agent/installer && .\build.ps1
```

## Version Compatibility

When updating versions:
1. Agent version: `agent/cmd/agent/main.go` (version const)
2. Agent version: `agent/internal/enrollment/client.go` (agentVersion)
3. Server version: `server/cmd/server/main.go` (@title, @version)
4. Frontend: N/A (runs against server API)

## Getting Help

- Swagger docs: `http://localhost:8080/api/docs`
- Agent metrics: `http://localhost:9183/metrics`
- Agent health: `http://localhost:9183/health`
- Docker logs: `docker-compose logs -f`

## Fleet Management & Auto-Registration

### Central Fleet Settings

The server maintains global configuration settings that are pushed to agents during heartbeat registration:

- **Heartbeat Interval**: How often agents report status (default 120s). Agents will adjust their own timers dynamically based on this value.
- **Update Interval**: How often agents refresh software mappings and inventory (default 1h).
- **Min Agent Version**: Used to track which agents need forced upgrades.
- **Stale Timeout**: Number of days of no contact before an agent is automatically purged from the database.

Settings are managed via **Agents > Settings** in the Web Portal.

### MSI Installer Customization

Installers can be pre-configured with the following properties:

- `SERVERADDRESS`: The full URL of the central server.
- `BUILDING`: Default building name for auto-lab assignment.
- `ROOM`: Default room number for auto-lab assignment.

If `BUILDING` and `ROOM` are provided, the server will automatically create the lab if it doesn't exist upon initial registration.

## Foreground Tracking Fix

### Problem
The foreground tracking metrics showed all 0s in Prometheus even though the code was implemented. This was because:
1. The WMI watcher only catches processes that start *after* the agent starts
2. Processes already running when the agent starts were not being tracked
3. When the foreground window belonged to an untracked process, the foreground time couldn't be attributed

### Solution
Added startup process scanning:
1. `agent/internal/monitor/wmi.go`: Added `ScanExistingProcesses()` function to query all running processes via WMI
2. `agent/internal/monitor/tracker.go`: Added `RegisterExistingProcess()` method to register processes with the tracker
3. `agent/cmd/agent/main.go`: Added startup scan that discovers and registers existing processes

This ensures that when the agent starts, it scans for all currently running processes and registers them with the tracker, allowing the foreground poller to properly attribute foreground time.
