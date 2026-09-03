package monitor

import "strings"

// Token elevation types (winnt.h TOKEN_ELEVATION_TYPE). golang.org/x/sys/windows
// exports the TokenElevationType info class but not these values, and this file
// is unit-tested on non-Windows platforms, so they are defined here untagged.
const (
	tokenElevationTypeDefault uint32 = 1 // no split token: built-in Administrator, SYSTEM, UAC disabled
	tokenElevationTypeFull    uint32 = 2 // elevated half of a UAC split token
	tokenElevationTypeLimited uint32 = 3 // filtered (ordinary) half of a UAC split token
)

// windowsAncestorLookup returns a pid's (elevationType, ppid, ok) — an
// indirection over the real token/process-tree lookups so
// findElevationBoundary is unit-testable with a fake process chain, no real
// Win32 calls required.
type windowsAncestorLookup func(pid uint32) (elevType, ppid uint32, ok bool)

// findElevationBoundary decides whether a TokenElevationTypeFull process
// represents a genuine new UAC consent, by walking up from startPID (the
// process's parent) looking for the first ancestor that is NOT Full.
//
// A one-hop check ("is the parent also Full?") isn't enough: when a process
// is created without a different token, the child simply reuses its
// parent's token object, so its own TokenElevationType reports the
// identical Full value — indistinguishable from a fresh UAC consent by
// looking at the type alone. The existing one-hop check tried to filter
// this by rejecting a Full child of a Full parent, but real-hardware
// testing rejected a *genuinely* UAC-approved process (procType Full, a
// live consent.exe fired moments before) this way: WMI's reported parent
// was also Full, one hop up from the true origin — likely a conpty/terminal
// hosting indirection. Mirrors macOS's findEscalatingAncestor exactly (same
// reasoning, opposite polarity: there a non-root uid proves a boundary,
// here a non-Full token type does).
//
// found=true (chainReadable implied true): a non-Full ancestor exists —
// this is a genuine new elevation. found=false, chainReadable=true: every
// ancestor up to maxAncestorHops was Full — ordinary token inheritance, not
// new. found=false, chainReadable=false: the chain became unreadable before
// resolving either way — callers should favor counting here, same bias as
// the original check (a UAC split token keeps the original user's
// identity, so attribution doesn't depend on this walk succeeding, unlike
// the macOS case where the parent IS the attribution).
func findElevationBoundary(startPID uint32, lookup windowsAncestorLookup) (found, chainReadable bool) {
	pid := startPID
	for i := 0; i < maxAncestorHops; i++ {
		elevType, ppid, ok := lookup(pid)
		if !ok {
			return false, false
		}
		if elevType != tokenElevationTypeFull {
			return true, true
		}
		if ppid == 0 || ppid == pid {
			return false, true
		}
		pid = ppid
	}
	return false, true
}

// procAncestorLookup returns a pid's (uid, ppid, ok) — an indirection over
// getProcBSDInfo so findEscalatingAncestor is unit-testable with a fake
// process chain, no real OS calls or genuine root privilege required.
type procAncestorLookup func(pid uint32) (uid, ppid uint32, ok bool)

// maxAncestorHops bounds the walk in findEscalatingAncestor. Real process
// trees resolve in 1-3 hops; this is generous headroom against unexpected
// process-tree shapes without risking an unbounded loop.
const maxAncestorHops = 8

// findEscalatingAncestor walks up the process tree from startPID looking for
// the first non-root ancestor — the human whose shell or app ultimately
// triggered a root-owned child.
//
// This can't stop at the immediate parent: modern sudo (1.8.15+, including
// macOS's) forks an internal "monitor" subprocess that calls setuid(0)
// *before* it execs the target command, so the immediate parent of a
// sudo'd process is sudo's own already-root plumbing, not the invoking
// shell. Stopping at one hop means every real sudo invocation looks
// identical to "a root process forked by another root process" (a daemon's
// own child, launchd's tree) — exactly the case that should NOT count. This
// was found by a real sudo command going completely uncounted: the process
// was observed and evaluated, just rejected by the one-hop check.
//
// Walking stops and returns found=false when: the chain becomes unreadable
// (no one to attribute to — same "don't count without a readable ancestor"
// rule as before, now applied to the whole chain instead of one hop), when
// an ancestor's ppid loops back on itself or hits 0 without ever finding a
// non-root uid (the chain terminates at launchd/init — a genuine root-owned
// tree, not a new escalation), or when maxAncestorHops is exceeded.
func findEscalatingAncestor(startPID uint32, lookup procAncestorLookup) (invokingUID uint32, found bool) {
	pid := startPID
	for i := 0; i < maxAncestorHops; i++ {
		uid, ppid, ok := lookup(pid)
		if !ok {
			return 0, false
		}
		if uid != 0 {
			return uid, true
		}
		if ppid == 0 || ppid == pid {
			return 0, false
		}
		pid = ppid
	}
	return 0, false
}

// incidentalSetuidTools are macOS binaries that ship setuid-root purely as an
// implementation detail (reading other users' process/network/scheduling
// state) and run that way on *every* invocation regardless of what they're
// asked to do — unlike sudo/su/login, running one isn't a deliberate request
// for elevated access in any sense a lab admin cares about. Found the hard
// way: a CI diagnostic step's own `ps -eo ...` call got counted as a real
// elevation, attributed to the account that happened to run it.
//
// This list is deliberately narrow (confirmed setuid-root on macOS) rather
// than reusing the general exclude-patterns mechanism, which exists for a
// different purpose (hiding noisy apps from usage tracking) and is
// per-deployment configurable — this is about a fixed OS-level fact, not a
// site's preference.
var incidentalSetuidTools = map[string]bool{
	"ps":          true,
	"top":         true,
	"traceroute":  true,
	"traceroute6": true,
	"at":          true,
	"atq":         true,
	"atrm":        true,
	"batch":       true,
	"crontab":     true,
	"quota":       true,
	"newgrp":      true,
}

// isIncidentalSetuidTool reports whether exeName is a known always-setuid
// utility that should never itself be counted as an elevation.
func isIncidentalSetuidTool(exeName string) bool {
	return incidentalSetuidTools[exeName]
}

// incidentalConsoleHosts are Windows pseudo-console host processes that ride
// along with an elevated shell as a side effect of one UAC consent, not a
// separate deliberate action — found the hard way: elevating a shell inside
// Windows Terminal counted twice, once for OpenConsole.exe (the conpty host)
// and once for the shell itself, both siblings under the same parent PID and
// both genuinely Full-token. A lab admin looking at "Top Elevated Apps"
// would see a confusing OpenConsole.exe entry for something no one chose to
// run. conhost.exe is the pre-Windows-Terminal equivalent, included for the
// same reason even though it wasn't observed directly in testing.
var incidentalConsoleHosts = map[string]bool{
	"openconsole.exe": true,
	"conhost.exe":     true,
}

// isIncidentalConsoleHost reports whether exeName is a known pseudo-console
// host that should never itself be counted as an elevation. exeName is
// lowercased before matching since Windows process names aren't
// case-normalized consistently at the source.
func isIncidentalConsoleHost(exeName string) bool {
	return incidentalConsoleHosts[strings.ToLower(exeName)]
}
