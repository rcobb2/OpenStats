# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

OpenLabStats is an open-source software usage tracking system for higher education labs — an alternative to proprietary tools like LabStats. It consists of three components: a cross-platform agent (Windows + macOS, Go), a central management server (Go), and a React/Vite web frontend. The agent and server are **separate Go modules** (`agent/go.mod`, `server/go.mod`) — run `go` commands from within each directory.

## Build & Run

```powershell
# Agent (Windows)
cd agent
go build -o openlabstats-agent.exe ./cmd/agent/
.\openlabstats-agent.exe --config configs\agent.yaml   # console/test mode
.\openlabstats-agent.exe install && net start OpenLabStats  # as service

# Agent (macOS)
cd agent
go build -o openlabstats-agent ./cmd/agent/
./openlabstats-agent --config configs/agent.yaml

# Server
cd server
go build -o server.exe ./cmd/server/
.\server.exe config\server.yaml

# Full stack via Docker (LOCAL DEV ONLY — never run against production)
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

## Deployment (Production)

Production runs on **podman02**. Deploys go through the **GitHub Action in the private
`rcobb2/OpenStats-deploy` repo** ("Deploy OpenStats to podman02") — never by hand on the host.

1. Merge to `master`. `.github/workflows/build.yml` publishes
   `ghcr.io/rcobb2/openstats-server:<full commit SHA>` (plus `:latest`).
2. In `OpenStats-deploy`, set `Image=` in `units/openstats-server.container` to that SHA
   (never `:latest`) and commit as `deploy: bump openstats-server to <short sha>`.
3. Push to `main`. The push triggers the workflow, which writes the unit file and the
   vendored Prometheus config, pulls the image and restarts `openstats-server` and
   `openstats-prometheus`. It can also be re-run with `workflow_dispatch`.
4. Verify with `openstatsctl version` — the `commit` field should be the new SHA.

- Do **not** ssh to podman02 to pull, build or restart anything; podman02 holds no
  checkout of the app.
- Prometheus config (`server/prometheus/*.yml`) is vendored into `OpenStats-deploy/prometheus/`.
  A change to those files needs the same copy there to go live.
- The surrounding infra (network, Postgres, Prometheus, Caddy, TLS) is managed outside
  this repo. `deploy/podman02/` is not read by the deploy workflow.
- Merging a server change does not make it live. The deploy is a separate step (and it
  restarts production, so confirm before pushing); say so explicitly instead of implying
  the change is already in production.

## Architecture

```
[Agent :9183] --/metrics--> [Prometheus] --> [Grafana]
        |
        +-- POST /api/v1/agents/register --> [Server :8080]
                                                  |
                                             PostgreSQL
                                             file_sd JSON (auto-updated)
                                             React SPA at /*
                                             Swagger at /api/docs
```

- **Agent** (`agent/`): Cross-platform service (Windows + macOS) that tracks process start/stop (WMI on Windows, polling on macOS), polls foreground window, normalizes software names, and exposes Prometheus metrics at `:9183/metrics`. Platform-specific entry points in `cmd/agent/agent_windows.go` and `cmd/agent/agent_darwin.go`; shared interface via `monitor/watcher.go`.
- **Server** (`server/`): REST API (chi router) backed by PostgreSQL; manages agent fleet, lab groupings, software name mappings, reports, and installer generation. Serves the frontend SPA and Swagger docs.
- **Frontend** (`server/web/`): React 19 + Vite SPA. All API calls go through `src/api.js`. Routes defined in `src/main.jsx`.

## Key Design Decisions & Behaviors

- **No auth** — designed for internal campus networks only. CORS is off by default (the SPA is same-origin; `cors.allowedOrigins` opts origins in), and `crossSiteGuard` rejects cross-site browser POST/PUT/PATCH/DELETE (`Sec-Fetch-Site`/`Origin`) so a page on another site can't drive the API from a staff browser. Agents, curl and openstatsctl send neither header and pass.
- **Metrics push is parsed, not passed through** — `POST /api/v1/agents/metrics` is parsed with expfmt (`metrics_ingest.go`); only `openlabstats_*` families are kept, `hostname` is forced to the registered agent's and agent-supplied `lab`/`building`/`room` are replaced by the server's. A malformed push is rejected (400) and the previous snapshot kept, so one bad agent can't break the single `/metrics/agents` scrape for the fleet. `/metrics/agents` merges families across agents with one HELP/TYPE each.
- **Agent hardening** — PE metadata is read only from local fixed drives (a UNC path would make SYSTEM authenticate to the host); macOS `Info.plist` reads refuse symlinks, FIFOs and files over 1 MiB. Self-update fails closed without `updateChecksum` and requires the server URL's scheme, host and port (redirects included). The checkpoint loop reconciles tracked PIDs against a live process list every tick so a lost exit event can't leave a group accruing usage forever.
- **Agent startup behavior**: `ScanExistingProcesses()` (in `wmi.go` on Windows, `proc_darwin.go` on macOS) runs at startup to register already-running processes — critical so the foreground poller can attribute time to processes launched before the agent started.
- **Two-tier name normalization** in the agent: (1) server-managed `software-map.json` looked up by exe name, (2) PE metadata (`FileDescription`) extracted from the executable. See `agent/internal/normalizer/`.
- **Prometheus service discovery**: The server writes a `file_sd` JSON file to `fileSD.outputPath` (configured in `server.yaml`). This file is auto-refreshed whenever lab/agent assignments change — no manual Prometheus config needed per agent.
- **Counters are not restored at startup** — Prometheus handles a counter resetting to 0; restoring persisted (lower) totals read as a false reset and double-counted usage. There is no local database. Because `increase()`/`rate()` can't count a series' first sample when it first appears non-zero, every counter is a `BaselineCounterVec`: a new label set is exposed at 0 and its first amount is held until the zero has been seen downstream, so the first event after a restart isn't lost. In production Prometheus scrapes the server's latest-snapshot store, not agents, so release is tied to *delivered* pushes (`Metrics.BeginDelivery`/`Delivered`, hooked into `PushMetrics`) and held >= 75s (`DefaultPushHold`); local `/metrics` reads release nothing. Only with no server URL (Prometheus scraping the agent directly) does `Metrics.ScrapeHandler` release after each scrape.
- **Session helper** (`agent/internal/session/`) — foreground app and input-idle time can only be read from inside the user's session (Windows service = session 0; macOS root daemon is outside the GUI session). The service spawns `openlabstats-agent session-helper` per user session (Windows: `CreateProcessAsUser` per active WTS session; macOS: `launchctl asuser` for the console user, then drops to that uid) and reads one JSON line per 5s over an anonymous stdout pipe, so a sample's origin is the process it launched, and PIDs are checked to belong to that session/uid. Helpers sit in a kill-on-close job object so they never lock the exe during an MSI upgrade. With no helper the poller falls back to the old in-process lookup (no idle data). Idle time at/above the threshold (`monitor.idleThreshold`, server `idleThresholdSeconds`) goes to `openlabstats_foreground_idle_seconds_total` instead of foreground time.
- **User filtering & correlation**: Usernames are resolved through `userid` before they reach a metric label. Built-in rules exclude system/service accounts (`SYSTEM`, `DWM`, `NT SERVICE`, computer accounts ending in `$`); admins add more ignore patterns (e.g. `zabbix`, `svc-*`) and identity aliases under **Users** in the portal. Domain stripping is on by default, so `COLGATE\jdoe`, `jdoe@colgate.edu`, and `jdoe` are one identity. The rules live in **two mirrored copies** — `agent/internal/userid/` (applied at collection) and `server/internal/userid/` (applied at query time, so pre-existing data is filtered and merged too). Keep them in sync; the separate modules prevent sharing. **Precedence**: built-in ignores first (aliases cannot rescue them), then admin ignore rules (beat any alias), then aliases most-specific-first (exact before wildcard, longer before shorter), else the canonical form.
- **Swagger docs** — Regenerate with `swag init` from `server/` after changing API handler annotations. Docs served at `/api/docs`.
- **Version constants** — Agent version is in `agent/cmd/agent/main.go` AND `agent/internal/enrollment/client.go`; keep both in sync.
- **Fleet Settings** — Server pushes global config to agents at registration: heartbeat interval, update interval, min agent version, stale timeout, idle threshold (`idleThresholdSeconds`, default 900, 0 disables). Managed via **Agents > Settings** in the web portal. The same response carries the **user policy** (ignore patterns, aliases, domain stripping) from **Users**.
- **Session metrics come from the OS, not from processes** — `agent/internal/logon/` enumerates logon sessions (WTS / utmpx) and is the only writer of `openlabstats_user_session_*`. The previous design inferred a logon from the first tracked process and a logoff from the last one exiting, which never fired again on machines that are never signed out (kiosk and service accounts), pinning every login counter at 1. Sessions already open at agent start are adopted, not counted, so a restart no longer manufactures logons.
- **User-keyed reports don't use PromQL `topk`** — identities are merged by canonical name in Go first, then ranked; ranking before the merge would double-count or drop a user split across platforms. See `sumByCanonicalUser` in `server/internal/api/reports.go`.
- **Range-based reports query Prometheus recording rules, not raw counters** — `?range=1h..30d` reports read the `openlabstats:*:rate15m` series (`server/prometheus/alerts.yml`'s `openlabstats_report_rollups` group) via `sum_over_time()`, not `increase()` on the raw counter directly. This fleet's cardinality makes a long-range `increase()` on raw data genuinely expensive (hundreds of millions of samples for a 30d query); the recording rule precomputes 15m deltas centrally so a report sums ~2,880 points instead. A new rule only accumulates from the moment it's added — there's no backfill against historical raw data (would need `promtool tsdb create-blocks-from rules` run directly against the Prometheus host), so a long-range report's completeness grows for ~30 days after this shipped. The server also caches/coalesces identical Prometheus queries for 60s (`cachingTransport` in `server/internal/api/router.go`) and caps concurrent Prometheus requests at 2 (`concurrencyLimitedTransport`, same file) — both defend against one report page's own multi-panel fan-out overwhelming Prometheus.

## Key Files

| Path | Purpose |
|------|---------|
| `agent/cmd/agent/main.go` | Entry point, CLI subcommands (version from `enrollment.AgentVersion`) |
| `agent/cmd/agent/agent_windows.go` | Windows agent runner (WMI-based) |
| `agent/cmd/agent/agent_darwin.go` | macOS agent runner (poll-based) |
| `agent/internal/monitor/watcher.go` | Cross-platform watcher interface (`WMIWatcherConfig`) |
| `agent/internal/monitor/wmi.go` | Windows WMI subscriptions + `ScanExistingProcesses()` |
| `agent/internal/monitor/proc_darwin.go` | macOS process scanning + `ScanExistingProcesses()` |
| `agent/internal/monitor/tracker.go` | Process state, foreground time attribution, user filtering |
| `agent/internal/monitor/foreground_poller.go` | Credits foreground time from session-helper samples (legacy in-process lookup as fallback), applies idle threshold |
| `agent/internal/metrics/prometheus.go` | All Prometheus metric definitions and labels |
| `agent/internal/normalizer/normalizer.go` | Name resolution orchestration |
| `agent/internal/logon/` | OS logon session tracking (WTS on Windows, utmpx on macOS); sole writer of `user_session_*` |
| `agent/internal/userid/` | Username canonicalization + ignore policy (mirror of server copy) |
| `agent/internal/enrollment/client.go` | Server registration + heartbeat; also holds `agentVersion` const |
| `agent/internal/session/` | Session helper (per-user-session foreground/idle sampling), supervisor and hub |
| `server/internal/api/router.go` | chi router, all routes, CORS, Swagger, SPA |
| `server/cmd/openstatsctl/` | CLI client for a live instance (reads + curated writes); driven by the `openstats` skill |
| `server/internal/api/users.go` | User rules, discovered-user listing, agent policy payload |
| `server/internal/userid/` | Username canonicalization, ignore policy, PromQL ignore matcher |
| `server/internal/store/postgres.go` | pgxpool connection, `migrate()`, all queries |
| `server/internal/discovery/file_sd.go` | Prometheus file_sd target generation |
| `server/web/src/api.js` | All frontend API calls |
| `server/web/src/main.jsx` | React Router routes |
| `server/web/src/components/Layout.jsx` | Nav structure |

## Interacting With a Live Instance

`openstatsctl` (in `server/cmd/openstatsctl/`) is the client for a running deployment — fleet status, reports, users, mappings, labs, plus low-risk writes (ignore/alias users, edit mappings, assign a lab). Build it onto PATH with `cd server && go build -o ../bin/openstatsctl ./cmd/openstatsctl/`. Server resolution: `--server`, then `$OPENSTATS_URL`, then the production default.

Deleting agents and forcing a single agent's update are deliberately **not** exposed — use the web portal. Staggered fleet auto-update *is* exposed via `openstatsctl rollout` (status/enable/disable/set; `enable` is confirm-gated) — the one deliberate settings-write; other settings writes remain portal-only. The `.claude/skills/openstats/` skill wraps the CLI and carries the interpretation caveats (login reports are sparse; shared-account hours exceed wall-clock; built-in ignores can't be cleared).

## API Contract (Agent ↔ Server)

- **Registration/heartbeat**: `POST /api/v1/agents/register` — payload: `{ id, hostname, ipAddress, osVersion, agentVersion, port, building, room }`; response: `{ agent, settings, updateUrl, updateChecksum, ignoredExeNames, userPolicy }`. `updateChecksum` is the server's SHA-256 of the file `updateUrl` points to (computed fresh per response, cached by file mtime/size); the agent verifies it before executing a downloaded installer and aborts on mismatch, skipping verification only when the server couldn't compute one.
- **User policy** (fetched once at agent startup): `GET /api/v1/users/policy`
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

### Changing User Filtering or Correlation
1. `server/internal/userid/` — rules and PromQL ignore matcher
2. `agent/internal/userid/` — mirror the same change
3. `server/internal/store/postgres.go` — `user_mappings` operations
4. `server/internal/api/users.go` — handlers / agent policy payload
5. `server/web/src/pages/Users.jsx` — admin UI
6. Bump `enrollment.AgentVersion` if agent behavior changed

### Adding a New Frontend Page
1. `server/web/src/pages/<Name>.jsx`
2. `server/web/src/main.jsx` (route)
3. `server/web/src/components/Layout.jsx` (nav link)
4. `server/web/src/api.js` (if new API calls needed)

### Modifying Database Schema
- Edit the `migrate()` function in `server/internal/store/postgres.go` — never in migrations files.

### Version Bump
- Single source of truth: `enrollment.AgentVersion` in `agent/internal/enrollment/client.go`. `main.go` references it — no second edit needed.
- The installer version is derived automatically: `agent/installer/build.ps1` greps `AgentVersion` and passes `-d Version=...` to WiX. No second edit needed. (There is no `openlabstats.wxs`; the manifest is `agent/installer/Package.wxs`.)

## MSI Installer

Silent install with auto-lab assignment:
```powershell
msiexec /i openlabstats-agent.msi /qn SERVERADDRESS="https://openstats.colgate.edu" BUILDING="Science Hall" ROOM="302"
```
If `BUILDING` and `ROOM` are provided and no matching lab exists, the server auto-creates it on first registration.

**`SERVERADDRESS` must include the scheme.** A bare hostname makes every agent request fail
with `unsupported protocol scheme ""`, so agents install successfully and never register —
this cost an entire Academic SCCM rollout. Newer agents normalize it as a safety net; agents
already in the field do not.

The MSI applies its properties by writing `HKLM\SOFTWARE\OpenLabStats` via native WiX
`RegistryValue`; the agent layers that over `agent.yaml` in
`agent/internal/config/override_windows.go`. Do **not** reintroduce a PowerShell custom
action to patch YAML — the previous one used `Return="ignore"`, so failures were invisible
and left agents unconfigured while msiexec reported success.

A successful msiexec exit code is not evidence of enrollment. Verify against
`/api/v1/agents` before scaling a deployment out. See `agent/agent.md` for the pilot checks.

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
- `server/web/web.md` — frontend structure, pages, components
