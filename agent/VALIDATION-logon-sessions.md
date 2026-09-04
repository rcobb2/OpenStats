# Validating OS logon session tracking (agent v0.2.0)

Commit **74d87dd** replaced how the agent produces user-session metrics. Sessions
now come from the operating system — `WTSEnumerateSessions` on Windows, `utmpx`
login records on macOS — instead of being inferred from process refcounts.

The tracker logic has unit tests that run anywhere. **The platform enumeration
code has never been executed.** It cross-compiles for Windows and passes
`go vet`, but struct layouts, cgo field names, and timestamp conversions cannot
be validated by a compiler. That is what this document is for.

Do not deploy this to the fleet until both platforms pass Part 3.

---

## Why it changed

Session metrics used to work like this: the first tracked process for a user
counted as a logon, the last one exiting counted as a logoff. On a machine that
is never signed out — a kiosk account, a service account — the refcount never
returned to zero, so exactly one logon was ever recorded.

On the live fleet, **204 of 205 login series read exactly `1`**. Every report
built on `increase(openlabstats_user_session_logins_total[24h])` returned
nothing, which is why three panels on the User Behavior dashboard showed "No data
for this period." An agent restart also re-counted every already-open session as
a fresh logon.

Two behaviors are therefore the heart of this validation:

1. A sign-in is counted **even when another account already has processes running**.
2. Sessions already open when the agent starts are **adopted, not counted**.

---

## Ground rules

These machines are in production. The installed `OpenLabStats` service is
running, enrolled, and pushing metrics.

- **Do not** stop, uninstall, or upgrade the installed service.
- **Do not** run `openlabstats-agent install` — you would clobber the production
  service registration.
- **Do** run the test build in console mode on a **different port** with a
  **separate database**, using the scratch config in Part 1. Two agents on one
  host is fine as long as they don't share port 9183 or a SQLite file.
- **Do** leave `reportURL` empty in the scratch config. The test agent then never
  registers, never pushes, and never touches server state. Consequence: only the
  *built-in* ignore rules apply, since server-managed rules and aliases arrive
  via registration. That is expected and correct for isolated testing.
- Report findings; don't fix them. If something fails, capture the evidence and
  hand it back rather than editing the enumerator — a wrong guess about a WTS
  struct layout is worse than a clear bug report.

---

## Part 1 — Environment prep

Both platforms need **Go 1.26.1 or newer** (`go.mod` pins `go 1.26.1`).

```bash
git -C <repo> fetch origin
git -C <repo> checkout 74d87dd     # or master, if it has moved on
git -C <repo> log --oneline -1     # expect: 74d87dd feat(agent): derive user sessions...
go version
```

### macOS

cgo is required — `enum_darwin.go` includes `<utmpx.h>`, and the existing
foreground poller links CoreGraphics.

```bash
xcode-select -p || xcode-select --install
cd agent
```

### Windows

No cgo needed; the WTS calls go through `syscall.MustLoadDLL`. PowerShell:

```powershell
cd agent
```

### Scratch config

Copy the platform config and override four fields. macOS
(`/tmp/olstest/agent.yaml`), Windows (`C:\olstest\agent.yaml`) — adjust paths to
taste:

```yaml
server:
  port: 9199            # NOT 9183 — the production agent owns that
  metricsPath: /metrics
  reportURL: ""         # no registration, no metrics push, no server state

monitor:
  reconcileInterval: 30s
  minLifetime: 2s
  building: ""
  room: ""
  excludePatterns: []

normalizer:
  mappingFile: configs/software-map.json
  mappingRefreshInterval: 1h

inventory:
  scanInterval: 1h

store:
  dbPath: /tmp/olstest/agent.db     # Windows: C:\olstest\agent.db

logging:
  level: debug
  filePath: /tmp/olstest/agent.log  # Windows: C:\olstest\agent.log
```

---

## Part 2 — Automated suites

Four test packages exist: `internal/logon`, `internal/userid`,
`internal/monitor`, `cmd/agent`. On Windows and macOS all four build; on Linux
three of them can't (platform files sit behind build tags), which is why this
handoff exists.

**macOS:**

```bash
cd agent
CGO_ENABLED=1 go build ./...
go vet ./...
go test ./... -v 2>&1 | tail -40
```

**Windows (PowerShell):**

```powershell
cd agent
go build ./...
go vet ./...
go test ./... -v 2>&1 | Select-Object -Last 40
```

Expected: `ok` for all four packages, no vet output. Record the full result
either way.

The logon suite alone, if you want it isolated:

```bash
go test ./internal/logon/ -v
```

It covers: adoption on first poll, a new logon after seeding, sign-out clearing
gauges, re-logon counting, concurrent sessions not double-billing time, ignored
accounts producing nothing, and a state change (RDP detach) not counting as a new
logon. These pass on Linux already — running them on the real platform confirms
nothing about the enumerator, only that nothing regressed.

**Also build the binary and confirm the version**, since a stale build is the
most common source of confusing results:

```bash
go build -o /tmp/olstest/agent ./cmd/agent/     # Windows: -o C:\olstest\agent.exe
/tmp/olstest/agent version                      # expect: openlabstats-agent v0.2.0
```

---

## Part 3 — Manual validation

This is the part that matters. Run the test agent in console mode:

```bash
/tmp/olstest/agent --config /tmp/olstest/agent.yaml
```

Read metrics from the scratch port in a second terminal:

```bash
curl -s localhost:9199/metrics | grep openlabstats_user_session
```

```powershell
(Invoke-WebRequest -UseBasicParsing http://localhost:9199/metrics).Content `
  -split "`n" | Select-String openlabstats_user_session
```

The agent's own endpoint carries only `user` and `hostname` labels; the
`lab`/`building`/`room` labels you see in Prometheus are added server-side.

The poll interval is **15 seconds**, so allow up to that long for any transition
to appear. A 15-second lag is expected behavior, not a finding.

### Scenario matrix

| # | Scenario | Platforms | Pass criteria |
|---|---|---|---|
| 1 | Your own session is visible | both | `user_session_active{user="<you>"} 1` present, username canonical (lowercase, no domain prefix) |
| 2 | Duration matches reality | both | `user_session_duration_seconds` within ~60s of your real logon time |
| 3 | Restart adoption | both | after restart: `active` = 1, and **no `logins_total` line at all** for you |
| 4 | Second user signs in | both | `logins_total{user="<userB>"}` = 1, your own counter unchanged |
| 5 | Second user signs out | both | their `active` → 0, `duration` → 0 |
| 6 | Re-logon counts again | both | their `logins_total` → 2 |
| 7 | Time accrues once, not twice | both | `session_seconds_total` grows ~60 per 60s per user |
| 8 | RDP detach ≠ sign-out | Windows | `active` stays 1 while disconnected; no new logon on reconnect |
| 9 | SSH login is a second session | macOS | `logins_total` +1 for the same user; `seconds_total` still ~60/60s |

### Scenario 3 — restart adoption (read this one carefully)

This is the specific bug that was fixed, and its pass condition is easy to
misread. Prometheus counters live in memory, so a restarted agent starts every
counter at zero. A counter that is never incremented **emits no line at all**.

1. With the agent running and you signed in, note `user_session_logins_total`.
2. Ctrl+C the agent. Restart it with the same command.
3. Wait 20 seconds and re-read the metrics.

| Result | Meaning |
|---|---|
| `active` = 1, **no** `logins_total` line for you | **Pass** — your session was adopted |
| `active` = 1, `logins_total` = 1 | **Fail** — the restart manufactured a logon (old bug) |
| no `active` line | **Fail** — enumeration found nothing; see Part 4 |

Also confirm `duration_seconds` after the restart still reflects your *real*
logon time rather than restarting from zero — that value comes from the OS, and
its survival across a restart is a second signal that adoption worked.

### Scenarios 4–6 — a second user

Console mode dies when its own user signs out, so drive these with a **second
account** while the agent keeps running in your session.

- **Windows:** sign in as a second user via fast user switching (Win+L → Switch
  user), or connect over RDP. Your console session stays alive, so the agent
  keeps polling. Sign that user out to test 5, sign back in for 6.
- **macOS:** System Settings → Users & Groups → fast user switching, then switch
  to a second local account. Or, for a lighter version of the same edges,
  `ssh localhost` from a terminal and `exit` — that exercises scenario 9 and the
  sign-out path in one go.

If no second account exists and you can't create one, mark 4–6 as **not run**
rather than improvising. The unit tests cover the logic; what's unverified here
is whether the OS enumeration *sees* the transition, and a fabricated test
doesn't answer that.

### Scenario 7 — time accrues once

The new tracker is the sole writer of these metrics. If the old refcount path
were somehow still live, time would accrue twice.

```bash
curl -s localhost:9199/metrics | grep session_seconds_total
sleep 60
curl -s localhost:9199/metrics | grep session_seconds_total
```

Delta per user should be **≈60** (±2). A delta near 120 means two writers —
report it immediately, it invalidates every session number.

---

## Part 4 — Prime suspects

Where I expect this to break, most likely first. Each entry names the symptom so
you can match it against what you see.

**1. `wtsInfoW` struct layout** (`internal/logon/enum_windows.go`)

The struct mirrors `WTSINFOW` by hand to reach `LogonTime`, the fourth of five
trailing `LARGE_INTEGER` timestamps. If the layout is off by a field, the
timestamp is read from the wrong offset.

*Symptom:* `duration_seconds` is absurd — negative, a value near 1.3e10 (epoch
confusion), or wildly larger than your uptime. Note that `queryWTSLogonTime`
returns the zero time on a short buffer, which the tracker treats as "started
now", so a *plausible but always-small* duration on a long-lived session is the
same bug wearing a disguise. Cross-check with `quser` (`query session`), which
prints the true LOGON TIME.

**2. Session visibility without privilege** (`queryWTSString`)

Querying *another* session's username generally requires elevation. Run as a
normal user you may see only your own session; the production agent runs as
LocalSystem and won't have this limit.

*Symptom:* scenario 4 shows no series for the second user. Before calling it a
bug, re-run the test agent from an **elevated** PowerShell and see whether the
second session appears. Report which of the two it was — that distinction
decides whether this matters in production at all.

**3. utmpx field names and constants** (`internal/logon/enum_darwin.go`)

`ut_user`, `ut_line`, `ut_tv.tv_sec`, and `C.USER_PROCESS` are read straight from
the SDK headers. These fail at *compile* time, so Part 2 catches them — a build
error mentioning `utmpx` is this, not a mystery.

**4. macOS GUI sessions come from `loginwindow`, not `utmpx`, now**

The console GUI session (and any Fast-User-Switched-in one) is detected by
enumerating running `loginwindow` processes, one per signed-in GUI user —
`utmpx`'s console line is deliberately skipped as unreliable for anything
beyond a single foreground user. `utmpx` is still consulted, but only for
non-console lines (SSH).

*Symptom:* a GUI session doesn't show up. Check `ps -axo user,comm | grep
loginwindow` — every signed-in GUI user (including ones switched into the
background via Fast User Switching) should have their own entry, owned by
them. If `ps` shows a real user's `loginwindow` process, and the tracker still
never counts a session, the bug is downstream (username resolution/policy),
not enumeration.

**5. `loginwindow`'s reported start time can be wildly wrong**

`loginwindow` launches before the clock has synced to NTP/RTC at boot, and
its libproc-reported start time (`pbi_start_tvsec`) freezes to that pre-sync
reading for its entire lifetime — even after `kern.boottime` itself is later
corrected. Observed on real hardware as a literal start time of 1976 on an
otherwise healthy Mac. `Tracker`'s `maxPlausibleSessionAge` guard catches this
(and the equivalent `utmpx`/RTC-battery failure mode) by substituting "now"
for anything more than 400 days in the past — this is expected, not a bug, if
you see it in the logs as `adopted existing user session ... since <now>`
right after a fresh boot.

**5. Session ID stability across polls**

Sessions are keyed on `(ID, RawUser)`. If a poll reports a *different* ID for the
same underlying session, every poll looks like a sign-out plus a sign-in.

*Symptom:* `logins_total` climbing steadily — one per 15-second poll — and
`duration_seconds` repeatedly resetting. This would be the worst outcome, since
it inflates the exact metric the change set out to fix. Watch for it during
scenario 7's two-minute window.

---

## Part 5 — What to report back

For each platform, hand back:

```
Platform:        macOS 15.x on Apple Silicon / Windows 11 23H2  (be specific)
Host:            hostname
Commit:          git log --oneline -1
Go version:      go version
Ran elevated:    yes / no

Part 2 — automated
  go build:      pass / fail   (paste output on failure)
  go vet:        pass / fail
  go test ./...: paste the full summary, all four packages

Part 3 — manual
  1 own session visible      pass / fail / not run
  2 duration matches         pass / fail / not run   (observed vs actual logon time)
  3 restart adoption         pass / fail / not run   (which of the three outcomes)
  4 second user signs in     pass / fail / not run
  5 second user signs out    pass / fail / not run
  6 re-logon counts          pass / fail / not run
  7 time accrues once        pass / fail / not run   (observed 60s delta per user)
  8 RDP detach   (Win)       pass / fail / not run
  9 SSH session  (mac)       pass / fail / not run

Evidence
  Two metric snapshots 60s apart, grepped to openlabstats_user_session.
  Relevant lines from the agent log (level: debug) — look for
  "adopted existing user session", "user session started", "user session ended".

Anything surprising that isn't in Part 4.
```

`not run` is a perfectly good answer with a reason attached. A guessed pass is
worse than a gap, because the whole point of this exercise is that nobody has
actually watched this code execute yet.

---

## Appendix — metric reference

| Metric | Type | Meaning after this change |
|---|---|---|
| `openlabstats_user_session_active` | Gauge | 1 while the user holds ≥1 OS logon session on this host |
| `openlabstats_user_session_duration_seconds` | Gauge | Seconds since the user's earliest currently-open session began, per the OS |
| `openlabstats_user_session_logins_total` | Counter | OS logon sessions observed since agent start, excluding those adopted at startup |
| `openlabstats_user_session_seconds_total` | Counter | Wall-clock seconds signed in, counted once per user across concurrent sessions |

All four carry `user` (canonical) and `hostname`. `user` is resolved through
`internal/userid`, so it is lowercase with any `DOMAIN\` prefix and `@domain`
suffix stripped.

**A semantic change worth knowing:** `user_session_seconds_total` now measures
*signed-in* time. It previously measured *time with at least one tracked process
running*. The new meaning is more accurate for lab occupancy, but any chart
spanning the cutover will show a discontinuity.

## Appendix — troubleshooting

| Symptom | Likely cause |
|---|---|
| `bind: address already in use` | The production agent owns 9183. Use 9199 in the scratch config. |
| No `user_session_*` lines at all | Enumeration returned nothing. Check the debug log for "logon enumeration failed". |
| `database is locked` | The scratch config is pointing at the production SQLite file. Give it its own `dbPath`. |
| Metrics look stale | 15-second poll interval; wait, then re-read. |
| Your username appears with a domain prefix | Canonicalization didn't run — that's a `userid` bug, not a logon bug. Report separately. |
