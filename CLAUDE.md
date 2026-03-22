# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

OpenLabStats is an open-source Windows software usage tracker (alternative to LabStats). Two Go modules: `agent/` (Windows service) and `server/` (central management server + React frontend).

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

**Agent startup behavior**: `wmi.go:ScanExistingProcesses()` runs at startup to register already-running processes — critical so the foreground poller can attribute time to processes launched before the agent started.

**Software name normalization** (two-tier): server-managed `configs/software-map.json` lookup first, then PE `FileDescription` from the executable binary.

**Prometheus service discovery**: The server writes a `file_sd` JSON file to `fileSD.outputPath` (configured in `server.yaml`). This file is auto-refreshed whenever lab/agent assignments change — no manual Prometheus config needed per agent.

**User filtering**: Agent only records metrics for human users — system accounts (`SYSTEM`, `DWM`, `NT SERVICE`, computer accounts ending in `$`) are excluded in `tracker.go`.

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
| `agent/internal/store/sqlite.go` | SQLite local persistence (sessions table) |
| `server/internal/api/router.go` | chi router, all routes, CORS, Swagger, SPA |
| `server/internal/store/postgres.go` | pgxpool connection, `migrate()`, all queries |
| `server/internal/discovery/file_sd.go` | Prometheus file_sd target generation |
| `server/web/src/api.js` | All frontend API calls |
| `server/web/src/main.jsx` | React Router routes |
| `server/web/src/components/Layout.jsx` | Nav structure |

## Ports

- `9183` — Agent metrics/health
- `8080` — Server (API + SPA)
- `5432` — PostgreSQL
- `9090` — Prometheus
- `3000` — Grafana

## Change Checklists

### New agent metric
`metrics/prometheus.go` → wire in `main.go` → `README.md` metrics table → `server/grafana/dashboards/*.json` → `deploy/grafana-dashboard.json`

### New API endpoint
`router.go` → `api/<entity>.go` → `store/postgres.go` → `server/docs/swagger.yaml` (run `swag init`) → `web/src/api.js` → page/component → `main.jsx` route → `Layout.jsx` nav link

### New frontend page
`web/src/pages/<Name>.jsx` → `main.jsx` → `Layout.jsx` → `web/src/api.js`

### DB schema change
Only in `store/postgres.go` `migrate()` — never in migrations files.

### Version bump
Both `agent/cmd/agent/main.go` (version const) AND `agent/internal/enrollment/client.go` (agentVersion).

## Fleet Settings

Server pushes global config to agents at registration: heartbeat interval, update interval, min agent version, stale timeout. Managed via **Agents > Settings** in the web portal.

## MSI Installer

Silent install with auto-lab assignment:
```powershell
msiexec /i openlabstats-agent.msi /qn SERVERADDRESS="http://server:8080" BUILDING="Science Hall" ROOM="302"
```
If `BUILDING`+`ROOM` are provided and no matching lab exists, the server auto-creates it on first registration.
