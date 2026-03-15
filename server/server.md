# Server Component Documentation

The OpenLabStats server is a Go HTTP service that manages agent enrollment, software mappings, lab groupings, and reporting. It also generates Prometheus target files for automatic service discovery.

## Overview

The server:
1. Provides REST API for agent registration and fleet management
2. Stores data in PostgreSQL
3. Writes Prometheus file_sd targets for agent discovery
4. Serves the React frontend (SPA)
5. Generates customized agent installers

## Key Packages

### `internal/api/router.go`

Main HTTP router using chi:
- Sets up CORS (open for dev)
- Registers all API routes
- Serves Swagger UI at `/api/docs`
- Serves frontend SPA at `/*`

### API Endpoints

| Route | Method | Description |
|-------|--------|-------------|
| `/api/v1/agents/register` | POST | Agent registration/heartbeat |
| `/api/v1/agents` | GET | List all agents |
| `/api/v1/agents/{id}` | GET | Get agent details |
| `/api/v1/agents/{id}/lab` | PUT | Assign agent to lab |
| `/api/v1/agents/{id}` | DELETE | Remove agent |
| `/api/v1/labs` | GET, POST | List/create labs |
| `/api/v1/labs/{id}` | GET, PUT, DELETE | Lab CRUD |
| `/api/v1/mappings` | GET, POST | List/create mappings |
| `/api/v1/mappings/agent` | GET | Agent-facing mapping JSON |
| `/api/v1/mappings` | PUT | Bulk update mappings |
| `/api/v1/reports/top-apps` | GET | Top applications by usage |
| `/api/v1/reports/usage-by-lab` | GET | Usage grouped by lab |
| `/api/v1/reports/active-users` | GET | Currently active users |
| `/api/v1/reports/summary` | GET | Overview statistics |
| `/api/v1/installers/generate` | POST | Generate custom installer |
| `/api/v1/settings` | GET | System settings |
| `/health` | GET | Health check |

### `internal/store/postgres.go`

PostgreSQL connection and migrations:
- Connects via pgxpool
- Auto-runs migrations on startup

**Tables:**
- `labs` - Lab/room definitions
- `agents` - Registered agents with status, lab assignment
- `software_mappings` - Software name normalization rules
- `installer_builds` - Generated installer records

### `internal/discovery/file_sd.go`

Prometheus file-based service discovery:
- `Refresh()` - Queries agents from DB, writes JSON to configured path
- Format: file_sd compatible JSON with `targets`, `labels`
- Includes: `__address__`, `lab`, `hostname`, `agent_version`

### `internal/api/agents.go`

Agent registration handler:
- Validates hostname
- Upserts agent to DB
- Triggers target refresh on change
- Handles heartbeat (re-registration)

### `internal/api/reports.go`

Report generation:
- Queries PostgreSQL for aggregated data
- Supports time range filtering (`?range=24h`)
- Returns JSON summaries

### `internal/api/installers.go`

Installer generation:
- Accepts server address, port, version
- Generates customized agent config
- (See server/web/web.md for frontend integration)

## Configuration (`config/server.yaml`)

```yaml
server:
  port: 8080
  host: ""

database:
  host: postgres
  port: 5432
  user: openlabstats
  password: openlabstats
  dbName: openlabstats
  sslMode: disable

prometheus:
  url: http://prometheus:9090

fileSD:
  outputPath: /etc/prometheus/file_sd/openlabstats.json
```

## Building

```powershell
cd server
go build -o server.exe ./cmd/server/
```

## Running

```powershell
# With config
.\server.exe config\server.yaml

# Via Docker
cd server
docker-compose up -d
```

## Database Schema

```sql
labs:
  id, name, building, room, description, created_at, updated_at

agents:
  id (PK), hostname, ip_address, os_version, agent_version,
  lab_id (FK), port, status, last_seen, created_at, updated_at

software_mappings:
  id, exe_name (UNIQUE), display_name, category, publisher,
  family, source, created_at, updated_at

installer_builds:
  id, server_address, port, version, filename, created_at
```

## Common Tasks

### Adding a New API Endpoint

1. Add route in `router.go`
2. Create handler in `internal/api/<entity>.go`
3. Add DB operations in `store/postgres.go`
4. Add Swagger annotations (or run `swag init`)
5. Add client function in `web/src/api.js`
6. Add frontend component if needed

### Modifying Agent Registration

- Handler: `internal/api/agents.go:RegisterAgent`
- Agent client: `agent/internal/enrollment/client.go`
- DB: `store/postgres.go` (UpsertAgent)
- Prometheus: `discovery/file_sd.go`

### Adding a Report

1. Add DB query in `store/postgres.go`
2. Add handler in `api/reports.go`
3. Add route in `router.go`
4. Add API function in frontend
5. Add UI component

## Dependencies

- Go 1.21+
- PostgreSQL
- Docker (for full stack)
- Node.js 18+ (for frontend dev)

## Ports

- `8080` - Server HTTP (API + frontend)
- `5432` - PostgreSQL (via Docker)
- `9090` - Prometheus (via Docker)
- `3000` - Grafana (via Docker)
