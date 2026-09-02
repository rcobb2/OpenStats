---
name: openstats
description: Query and administer a live OpenStats (OpenLabStats) instance — fleet status, agents, labs, usage and session reports, software mappings, and user ignore/correlation rules. Use for questions like "which apps are most used", "is the fleet healthy", "why is this user showing up", "ignore this service account", "what version is deployed", or any request to inspect or adjust a running OpenStats deployment.
---

# OpenStats live instance

Drive a running OpenStats deployment through `openstatsctl`, a CLI client for the
server's REST API. Prefer it over hand-written `curl`: it resolves the server,
formats compactly, and returns actionable errors with non-zero exit codes.

## Setup

The binary lives in the repo's `bin/`, which is on PATH. Build it if missing:

```bash
cd server && go build -o ../bin/openstatsctl ./cmd/openstatsctl/
```

Server resolution, in order: `--server URL`, `$OPENSTATS_URL`, then the default
`https://openstats.colgate.edu`. **The default is production.** Pass `--server`
explicitly when working against anything else.

Add `--json` to any command for raw output; the default is an aligned table,
which is usually what you want in a transcript.

## Reading

```bash
openstatsctl status                       # fleet overview + deployed version/commit
openstatsctl version                      # server build info

openstatsctl agents list                  # add --status online | --lab Alumni
openstatsctl agents get <hostname>        # full record for one agent
openstatsctl labs list

openstatsctl reports list                 # what's available, with units
openstatsctl reports top-apps --range 7d --limit 10
openstatsctl reports top-users-by-session-time --range 30d
openstatsctl reports usage-by-lab --range 24h --lab "Alumni"

openstatsctl users list --range 30d       # add --ignored or --tracked
openstatsctl users rules                  # ignore/correlation rules, with IDs
openstatsctl users policy                 # what agents actually receive
openstatsctl mappings list --review       # auto-discovered apps needing a name
openstatsctl settings get
```

Report flags: `--range` (`24h`, `7d`, `30d`), `--limit`, `--lab`, `--hostname`,
or `--start`/`--end` for a custom window.

## Writing

These take effect on the server immediately; agents pick up user-policy changes
on their next heartbeat (default 120s).

```bash
openstatsctl users ignore zabbix          # stop tracking an account; supports svc-*
openstatsctl users ignore 'svc-*'
openstatsctl users unignore zabbix        # clear an ignore rule
openstatsctl users alias jdoe2 jdoe       # merge a second username into one identity
openstatsctl users rules rm <id>          # delete a rule outright

openstatsctl mappings set EXCEL.EXE --name "Microsoft Excel" --category Productivity
openstatsctl mappings ignore "Some Junk Process"
openstatsctl mappings unignore "Some Junk Process"

openstatsctl agents assign <hostname> --lab <lab-id>
```

Confirm with the user before writing when the request is ambiguous about which
account or app is meant — an ignore rule silently removes a user from every
report, and that is easy to misattribute later.

**Not available on purpose:** deleting agents, forcing agent updates, and writing
fleet settings. If asked, point at the web portal rather than reaching for
`curl` — these are disruptive and easy to trigger from a mistaken premise.

## What to know before interpreting the numbers

**Login-based reports are sparse.** `top-users-by-logins`,
`top-devices-by-sessions`, and `avg-session-time` all derive from
`increase(user_session_logins_total[range])`. Lab machines are frequently never
signed out (kiosk accounts, service accounts), so a 24h window often returns
nothing at all while 30d returns a handful of rows. Empty output there is usually
correct, not a bug — reach for `--range 30d`, and prefer
`top-users-by-session-time` when the question is really "who uses these machines
the most".

**Elevation reports are Windows-only and sparse.** `top-apps-by-elevations` and
`top-users-by-elevations` count UAC-elevated process launches, which only
Windows agents v0.3.0+ emit and which are rare events — empty output on a short
range is expected, try `--range 30d`. macOS machines never contribute. The count
is attributed to the account whose credentials were used (an over-the-shoulder
IT elevation shows the admin account, not the student at the keyboard).

**Session-time totals exceed wall-clock.** A shared account signed in on 40
machines accrues 40 hours per hour. Numbers in the hundreds or thousands of hours
over 30 days are expected for accounts like `pubref`, not a data error.

**Ignored ≠ absent from history.** Ignore rules filter at query time as well as
at collection time, so an ignored account disappears from reports immediately,
including from data collected before the rule existed. `users list` still shows
it with `IGNORED yes`.

**Built-in ignores can't be cleared.** System and service accounts (`SYSTEM`,
`root`, `_*`, `Window Manager\DWM-*`, machine accounts ending in `$`) are
filtered by defaults compiled into both the agent and server. `users unignore`
will refuse and suggest an explicit alias, which is the correct escape hatch.

**Usernames are canonical.** `COLGATE\JDoe`, `jdoe@colgate.edu`, and `jdoe`
resolve to one identity, `jdoe`. `users list` shows the raw forms that were
merged under `SEEN AS`.

**Two versions, not one.** `openstatsctl version` reports the *server* build.
Agent versions appear in `agents list` and lag the server: agents self-update
only inside the fleet maintenance window, so a freshly deployed server routinely
sits alongside agents on an older release. A `version` field of `0.1.3` is a
known cosmetic artifact of the deploy playbook not passing `VERSION`; trust the
commit hash instead.

## Where things live

| Path | What |
|---|---|
| `server/cmd/openstatsctl/` | This CLI |
| `server/internal/api/` | REST handlers; `router.go` has every route |
| `server/internal/userid/` | Username canonicalization + ignore policy (query side) |
| `agent/internal/userid/` | The same rules, applied at collection time |
| `agent/internal/logon/` | OS logon session tracking |
| `server/docs/swagger.yaml` | Full API reference, also served at `/api/docs` |

For endpoints the CLI doesn't wrap, read `router.go` and use `curl` directly —
but check first whether a subcommand already covers it.
