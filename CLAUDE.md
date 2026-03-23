# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

OpenLabStats is an open-source software usage tracking system for higher education labs — an alternative to proprietary tools like LabStats. It consists of three components: a Windows agent (Go), a central management server (Go), and a React/Vite web frontend.

## Architecture

```
[Windows Agent] --register/heartbeat--> [Central Server (Go + PostgreSQL)]
[Windows Agent] --/metrics-----------> [Prometheus] --> [Grafana]
[Central Server] --file_sd-----------> [Prometheus targets]
[Central Server] --serves-----------> [React SPA]
```

- **Agent** (`agent/`): Windows service that uses WMI subscriptions to track process start/stop, polls foreground window, normalizes software names, and exposes Prometheus metrics at `:9183/metrics`.
- **Server** (`server/`): REST API (chi router) backed by PostgreSQL; manages agent fleet, lab groupings, software name mappings, reports, and installer generation. Serves the frontend SPA and Swagger docs.
- **Frontend** (`server/web/`): React 19 + Vite SPA. All API calls go through `src/api.js`. Routes defined in `src/main.jsx`.

The agent and server are **separate Go modules** (`agent/go.mod`, `server/go.mod`) — run `go` commands from within each directory.

## Key Design Decisions

- **No auth** — CORS is open; designed for internal campus networks only.
- **Two-tier name normalization** in the agent: (1) server-managed `software-map.json` looked up by exe name, (2) PE metadata (`FileDescription`) extracted from the executable. See `agent/internal/normalizer/`.
- **Prometheus file_sd** — The server writes a JSON file at `fileSD.outputPath` that Prometheus reads for agent target discovery. Refreshes automatically when agent registrations or lab assignments change.
- **SQLite on agent** — Local persistence for metric restoration across restarts (`agent/internal/store/sqlite.go`).
- **Swagger docs** — Regenerate with `swag init` from `server/` after changing API handler annotations. Docs served at `/api/docs`.
- **Version constants** — Agent version is in `agent/cmd/agent/main.go` AND `agent/internal/enrollment/client.go`; keep both in sync.

## Development Commands

### Agent (run from `agent/`)
```powershell
go build -o openlabstats-agent.exe ./cmd/agent/
go test ./...

# Console mode (Windows only — requires WMI)
.\openlabstats-agent.exe --config configs\agent.yaml
```

### Server (run from `server/`)
```powershell
go build -o server.exe ./cmd/server/
go test ./...

# Run with config
go run ./cmd/server/ --config config/server.yaml

# Full stack via Docker
docker-compose up -d
```

### Frontend (run from `server/web/`)
```bash
npm install
npm run dev      # Dev server
npm run build    # Production build
```

### MSI Installer (run from `agent/installer/`)
```powershell
.\build.ps1
```

## API Contract (Agent ↔ Server)

- **Registration/heartbeat**: `POST /api/v1/agents/register` — payload: `{ id, hostname, ipAddress, osVersion, agentVersion, port, building, room }`
- All server API routes are under `/api/v1/*` returning JSON
- Agent metrics at `http://<agent>:9183/metrics`, health at `http://<agent>:9183/health`

## Change Checklists

### Adding a New API Endpoint
1. `server/internal/api/router.go` — add route
2. `server/internal/api/<entity>.go` — add handler
3. `server/internal/store/postgres.go` — add DB operations
4. Run `swag init` in `server/` to regenerate Swagger docs
5. `server/web/src/api.js` — add client function
6. `server/web/src/pages/` and `src/main.jsx` / `Layout.jsx` if a new page/nav link is needed

### Adding a New Agent Metric
1. `agent/internal/metrics/prometheus.go` — define metric
2. `agent/cmd/agent/main.go` — wire up
3. `README.md` — update metrics table
4. `server/grafana/dashboards/*.json` and `deploy/grafana-dashboard.json` — update dashboards (keep in sync)

### Modifying Database Schema
- Edit the `migrate()` function in `server/internal/store/postgres.go`

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
