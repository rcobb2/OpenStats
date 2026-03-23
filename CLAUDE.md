# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

OpenLabStats is an open-source software usage tracking system for higher education labs — an alternative to proprietary tools like LabStats. It consists of three components: a Windows agent (Go), a central management server (Go), and a React/Vite web frontend. The agent and server are **separate Go modules** (`agent/go.mod`, `server/go.mod`) — run `go` commands from within each directory.

## Build & Run

```powershell
# Agent (must target Windows — uses WMI, Win32 APIs)
cd agent
go build -o openlabstats-agent.exe ./cmd/agent/
.\openlabstats-agent.exe --config configs\agent.yaml   # console/test mode
.\openlabstats-agent.exe install && net start OpenLabStats  # as service

# Server
cd server
go build -o server.exe ./cmd/server/
.\server.exe config\server.yaml

# Full stack via Docker
cd server && docker-compose up -d

# Frontend dev
cd server/web && npm install && npm run dev
npm run build  # production build (output embedded in server binary)

# MSI installer
cd agent/installer && .\build.ps1
```

## Tests

```powershell
cd agent && go test ./...
cd server && go test ./...
```

## Architecture

```
[Windows Agent :9183] --/metrics--> [Prometheus] --> [Grafana]
        |
        +-- POST /api/v1/agents/register --> [Server :8080]
                                                  |
                                             PostgreSQL
                                             file_sd JSON (auto-updated)
                                             React SPA at /*
                                             Swagger at /api/docs
```

- **Agent** (`agent/`): Windows service that uses WMI subscriptions to track process start/stop, polls foreground window, normalizes software names, and exposes Prometheus metrics at `:9183/metrics`.
- **Server** (`server/`): REST API (chi router) backed by PostgreSQL; manages agent fleet, lab groupings, software name mappings, reports, and installer generation. Serves the frontend SPA and Swagger docs.
- **Frontend** (`server/web/`): React 19 + Vite SPA. All API calls go through `src/api.js`. Routes defined in `src/main.jsx`.

## Key Design Decisions & Behaviors

- **No auth** — CORS is open; designed for internal campus networks only.
- **Agent startup behavior**: `wmi.go:ScanExistingProcesses()` runs at startup to register already-running processes — critical so the foreground poller can attribute time to processes launched before the agent started.
- **Two-tier name normalization** in the agent: (1) server-managed `software-map.json` looked up by exe name, (2) PE metadata (`FileDescription`) extracted from the executable. See `agent/internal/normalizer/`.
- **Prometheus service discovery**: The server writes a `file_sd` JSON file to `fileSD.outputPath` (configured in `server.yaml`). This file is auto-refreshed whenever lab/agent assignments change — no manual Prometheus config needed per agent.
- **SQLite on agent** — Local persistence for metric restoration across restarts (`agent/internal/store/sqlite.go`).
- **User filtering**: Agent only records metrics for human users — system accounts (`SYSTEM`, `DWM`, `NT SERVICE`, computer accounts ending in `$`) are excluded in `tracker.go`.
- **Swagger docs** — Regenerate with `swag init` from `server/` after changing API handler annotations. Docs served at `/api/docs`.
- **Version constants** — Agent version is in `agent/cmd/agent/main.go` AND `agent/internal/enrollment/client.go`; keep both in sync.
- **Fleet Settings** — Server pushes global config to agents at registration: heartbeat interval, update interval, min agent version, stale timeout. Managed via **Agents > Settings** in the web portal.

## Key Files

| Path | Purpose |
|------|---------|
| `agent/cmd/agent/main.go` | Entry point, CLI subcommands, version const (`v0.1.3`) |
| `agent/internal/monitor/wmi.go` | WMI subscriptions + `ScanExistingProcesses()` |
| `agent/internal/monitor/tracker.go` | Process state, foreground time attribution, user filtering |
| `agent/internal/monitor/foreground.go` | Win32 `GetForegroundWindow` polling |
| `agent/internal/metrics/prometheus.go` | All Prometheus metric definitions and labels |
| `agent/internal/normalizer/normalizer.go` | Name resolution orchestration |
| `agent/internal/enrollment/client.go` | Server registration + heartbeat; also holds `agentVersion` const |
| `agent/internal/store/sqlite.go` | SQLite local persistence |
| `server/internal/api/router.go` | chi router, all routes, CORS, Swagger, SPA |
| `server/internal/store/postgres.go` | pgxpool connection, `migrate()`, all queries |
| `server/internal/discovery/file_sd.go` | Prometheus file_sd target generation |
| `server/web/src/api.js` | All frontend API calls |
| `server/web/src/main.jsx` | React Router routes |
| `server/web/src/components/Layout.jsx` | Nav structure |

## API Contract (Agent ↔ Server)

- **Registration/heartbeat**: `POST /api/v1/agents/register` — payload: `{ id, hostname, ipAddress, osVersion, agentVersion, port, building, room }`
- All server API routes are under `/api/v1/*` returning JSON
- Agent metrics at `http://<agent>:9183/metrics`, health at `http://<agent>:9183/health`

## Change Checklists

### Adding a New Agent Metric
1. `agent/internal/metrics/prometheus.go` — define metric
2. `agent/cmd/agent/main.go` — wire up
3. `README.md` — update metrics table
4. `server/grafana/dashboards/*.json` and `deploy/grafana-dashboard.json` — update dashboards (keep in sync)

### Adding a New API Endpoint
1. `server/internal/api/router.go` — add route
2. `server/internal/api/<entity>.go` — add handler
3. `server/internal/store/postgres.go` — add DB operations
4. Run `swag init` in `server/` to regenerate Swagger docs
5. `server/docs/swagger.yaml` — regenerated docs
6. `server/web/src/api.js` — add client function
7. `server/web/src/pages/` and `server/web/src/main.jsx` / `server/web/src/components/Layout.jsx` if a new page/nav link is needed

### Adding a New Frontend Page
1. `server/web/src/pages/<Name>.jsx`
2. `server/web/src/main.jsx` (route)
3. `server/web/src/components/Layout.jsx` (nav link)
4. `server/web/src/api.js` (if new API calls needed)

### Modifying Database Schema
- Edit the `migrate()` function in `server/internal/store/postgres.go` — never in migrations files.

### Version Bump
- Update version in BOTH `agent/cmd/agent/main.go` (version const) AND `agent/internal/enrollment/client.go` (agentVersion).

## MSI Installer

Silent install with auto-lab assignment:
```powershell
msiexec /i openlabstats-agent.msi /qn SERVERADDRESS="http://server:8080" BUILDING="Science Hall" ROOM="302"
```
If `BUILDING` and `ROOM` are provided and no matching lab exists, the server auto-creates it on first registration.

## Ports (Docker stack)

| Port | Service |
|------|---------|
| 8080 | Server API + frontend |
| 9183 | Agent metrics |
| 5432 | PostgreSQL |
| 9090 | Prometheus |
| 3000 | Grafana |

## Documentation Files

Each component has its own context doc — update these when making significant changes:
- `HUMANS.md` — overall architecture
- `AGENTS.md` — AI agent coordination rules and change checklists
- `agent/agent.md` — agent internals, metrics, CLI tools
- `server/server.md` — server API, DB schema, components
