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
| `openlabstats_privilege_elevations_total` | Counter | app, exe, category, user, hostname |
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
- Emits events via callbacks: `OnStart`, `OnStop`, `OnElevated`

### `internal/monitor/elevation.go` / `elevation_windows.go` / `elevation_darwin.go`

Privilege-elevation detection. `elevation.go` holds the pure, platform-agnostic
decision logic (unit-tested on any OS); the platform files supply the actual
process-token / process-owner lookups.

**Windows** (`elevation_windows.go`): on each process start event, opens the
process token and reads `TOKEN_ELEVATION_TYPE`. Only `TokenElevationTypeFull`
(the elevated half of a UAC split token) counts — `Default` tokens (built-in
Administrator, SYSTEM, UAC-off machines) are always-elevated without a consent
event and are ignored. Children of an elevated process inherit the Full token,
so the parent's token is also checked: a Full child of a Full parent is not
counted. If the parent is gone or unreadable, the launch is still counted
(favor visibility) — a UAC split token keeps the original user's identity, so
attribution never depends on the parent.

**macOS** (`elevation_darwin.go`): on each poll cycle, a newly-started process
running as uid 0 with a non-root parent is a genuine escalation (`sudo`, an
admin AppleScript, an installer's root helper); a root process forked by
another root process (a daemon's own child, launchd's tree) is not. Unlike
Windows, `sudo` fully replaces the process owner with root, so the elevated
process can never be attributed to a human — attribution comes from the
**parent's** owner instead, and if the parent can no longer be inspected there
is no one to attribute to, so the launch is *not* counted (the opposite bias
from Windows). This check runs inside `currentSnapshot()`'s per-PID loop
*before* the usual exclude/system-path filtering, because the most common
escalation targets (`sudo softwareupdate`, `sudo launchctl`, `/usr/sbin/*`,
`/usr/bin/*`) are exactly the paths that filtering treats as noise for usage
tracking.

**macOS requires the agent to run as root.** `proc_pidinfo(PROC_PIDTBSDINFO)` —
what `getProcBSDInfo` calls — only succeeds for a process the caller owns or
when the caller is root; an unprivileged caller gets `ok=false` for *any*
other user's process, root's included (see
`TestGetProcBSDInfoCrossUserRequiresRoot`). This isn't a gap in production —
the agent already runs as root via a LaunchDaemon, which is also why it can
see every user's app usage on a shared lab Mac in the first place — but it
means an agent run unprivileged (ad hoc local testing, a misconfigured
install) silently detects zero elevations, the same way it silently misses
other users' usage. This cost real debugging time once: a CI smoke test
running the agent unprivileged saw a genuine `sudo` command produce nothing
at all, no error, no log line — see `root0Seen` in `currentSnapshot`'s debug
logging, a cheap field signal for exactly this failure mode.

Both platforms share these properties:
- Elevations are counted at **start time** via `OnElevated`, not in the OnStop
  session path — a short-lived elevated installer must count even if it dies
  before `minLifetime`. A cancelled UAC prompt or `sudo` password rejection
  never creates a process, so it is never counted.
- Processes already elevated when the agent starts are adopted, not counted
  (`ScanExistingProcesses` doesn't run the check), so a restart never
  manufactures elevation events.
- Persisted in SQLite (`app_usage_totals.total_elevations`) and restored across
  restarts like the other app counters.

### `internal/monitor/tracker.go`

Active process state management:
- Maintains map of running processes
- Handles checkpointing for metrics aggregation
- Deduplicates by (app, user, hostname) to prevent double-counting
- **User Filtering**: Only records metrics for valid human users (excludes system/computer accounts) — see `internal/userid/`
- Integrates with foreground poller

### `internal/logon/`

OS logon session tracking — the source of truth for the four `user_session_*`
metrics:

- `Enumerator` is platform-specific: `WTSEnumerateSessions` on Windows (console,
  RDP, and disconnected sessions), `utmpx` login records on macOS (console plus
  SSH). `enum_other.go` is a no-op stub so the package builds and its tests run
  on any OS.
- `Tracker` diffs each poll against the last and drives the metrics. It counts a
  logon only after the first poll has seeded state: sessions already open when
  the agent starts are **adopted**, not counted. `LoginTime` comes from the OS, so
  session duration survives an agent restart.
- Time accrues once per canonical user even when they hold several OS sessions
  (console + SSH is one occupant).

This replaced a refcount proxy that inferred a logon from the first tracked
process and a logoff from the last one exiting. On machines that are never signed
out — kiosk accounts, service accounts — the refcount never returned to zero, so
exactly one logon was ever recorded and every report built on
`increase(user_session_logins_total[...])` came back empty.

### `internal/userid/`

Username resolution, applied before any username reaches a metric label:
- `Canonicalize` strips a `DOMAIN\` prefix and an `@domain` suffix and lowercases,
  so a Windows domain account (`COLGATE\jdoe`) and its macOS counterpart (`jdoe`)
  share one time series.
- `Policy.Resolve` returns the canonical username and whether the account is
  ignored. Built-in defaults cover OS and service accounts; the server pushes
  additional ignore patterns and aliases on top.
- `Holder` guards the active policy — the server replaces it on every heartbeat
  while process events resolve usernames concurrently.

The same rules exist in `server/internal/userid/` so that what the agent drops at
collection time and what reports filter at query time agree. Keep them in sync.

In `cmd/agent/helpers.go`, `resolveUser` is the only function collection code
should call: it returns the canonical name to label with, or false to skip.
`isValidUser` applies the built-in rules only and exists for the startup path
and tests.

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
- Receives: fleet settings, update URL, ignored exe names, and the **user policy**
  (`stripDomain`, `ignorePatterns`, `aliases`), which is applied via the handler
  registered with `WithUserPolicyHandler`
- `FetchUserPolicy()` - GET `/api/v1/users/policy`, called once at startup so
  already-running processes are attributed correctly before the first heartbeat

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

These are bare subcommands, not flags — `main.go` matches `os.Args[1]` literally, so a
dash-prefixed form like `--status` is not recognized and instead starts the agent in
console mode.

| Command | Description |
|---------|-------------|
| `version` | Print agent version |
| `serveraddress` | Print configured server URL (after registry overrides) |
| `building` | Print configured building |
| `room` | Print configured room |
| `heartbeat` | Print heartbeat interval (from server settings) |
| `maintenancewindow` | Print maintenance window status and configured times |
| `setmaintenance <val>` | Set maintenance override (`true`, `false`, or `auto`) |
| `status` | Print full agent status (version, building, room, server, heartbeat, maintenance) |

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
| `SERVERADDRESS` | URL of the central management server — **must include the scheme** | `""` |
| `PORT` | Prometheus metrics scrape port | `9183` |
| `BUILDING` | Lab building name for auto-assignment | `""` |
| `ROOM` | Lab room number for auto-assignment | `""` |
| `MAPPINGUPDATEURL` | Override the mapping URL (derived from `SERVERADDRESS` by default) | `""` |
| `INSTALLDIR` | Custom installation path | `C:\Program Files\OpenLabStats` |

### Silent Install Examples

```powershell
# Standard install with server enrollment
msiexec /i openlabstats-agent.msi /qn SERVERADDRESS="https://openlabstats.campus.edu"

# Install with automatic lab and room assignment
msiexec /i openlabstats-agent.msi /qn SERVERADDRESS="https://server.campus.edu" BUILDING="Science Hall" ROOM="302"
```

### How Configuration Is Applied

The MSI records its properties under `HKLM\SOFTWARE\OpenLabStats` using native WiX
`RegistryValue` elements. At startup the agent reads that key and layers the values over
`agent.yaml` (`internal/config/override_windows.go`); a non-empty registry value wins,
and a missing key is normal for hand-configured installs.

| Registry value | From property |
|---|---|
| `ServerAddress` | `SERVERADDRESS` |
| `MappingUpdateURL` | `MAPPINGUPDATEURL`, else `SERVERADDRESS` + `/api/v1/mappings/agent` |
| `Port` | `PORT` |
| `Building` | `BUILDING` |
| `Room` | `ROOM` |
| `InstalledVersion` | `ProductVersion` (recorded for support) |

This replaced a deferred PowerShell custom action that regex-patched `agent.yaml` with
`Return="ignore"`. Because failures were ignored, a blocked or failing PowerShell left
`reportURL` empty while msiexec still reported success — the agent installed, the service
started, and it silently never registered. Registry writes are transactional and roll back.

### Fleet Deployment (SCCM / Intune / GPO)

> **`SERVERADDRESS` must include the scheme.** A bare hostname is the one mistake that
> reliably produces a fleet of agents that install successfully and never register: every
> request fails with `unsupported protocol scheme ""`, and the failure is only visible in
> the agent's own log. An Academic SCCM rollout was lost to exactly this.

```powershell
msiexec /i "openlabstats-agent-<version>.msi" /qn REBOOT=ReallySuppress ^
    SERVERADDRESS="https://openstats.colgate.edu" ^
    BUILDING="Case Library" ROOM="204" ^
    /l*v C:\Windows\Temp\openlabstats-install.log
```

Recent agents normalize a scheme-less address as a safety net, but do not rely on it —
older agents already in the field cannot.

**Verify a pilot machine before rolling out to a collection:**

```powershell
# 1. The installer recorded what you passed
reg query "HKLM\SOFTWARE\OpenLabStats"

# 2. The agent resolved it (should print an absolute URL)
& "C:\Program Files\OpenLabStats\openlabstats-agent.exe" serveraddress

# 3. The server saw it — the host should appear with a fresh lastSeen
curl "https://openstats.colgate.edu/api/v1/agents"
```

A successful `msiexec` exit code alone is **not** evidence of enrollment. Confirm the
machine appears in the portal before scaling out.

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

### Changing how sessions are counted

1. `internal/logon/tracker.go` — diff/metric logic, covered by tests that run on
   any platform
2. `internal/logon/enum_windows.go` / `enum_darwin.go` — platform enumeration;
   these need a real Windows or macOS host to validate, not just a cross-compile
3. Session metrics have exactly one writer. Nothing outside this package may
   touch `UserSession*`, or a session gets counted twice.

### Changing user filtering or correlation

1. `internal/userid/` — resolution rules (mirror the change in `server/internal/userid/`)
2. `cmd/agent/helpers.go` — `resolveUser` is the single entry point for collection code
3. Ignore lists and aliases are **server-managed** — add them in the web portal
   under **Users**, not in agent code

### Adding Exclude Pattern

Add regex to `monitor.excludePatterns` in `configs/agent.yaml`

### Modifying Software Mapping

Edit `configs/software-map.json` or manage via server UI
