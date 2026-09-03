package monitor

// Token elevation types (winnt.h TOKEN_ELEVATION_TYPE). golang.org/x/sys/windows
// exports the TokenElevationType info class but not these values, and this file
// is unit-tested on non-Windows platforms, so they are defined here untagged.
const (
	tokenElevationTypeDefault uint32 = 1 // no split token: built-in Administrator, SYSTEM, UAC disabled
	tokenElevationTypeFull    uint32 = 2 // elevated half of a UAC split token
	tokenElevationTypeLimited uint32 = 3 // filtered (ordinary) half of a UAC split token
)

// shouldCountElevation decides whether a process start represents a user-driven
// UAC elevation. Only TokenElevationTypeFull counts — a Default token (built-in
// Administrator, service accounts, UAC-off machines) is elevated without any
// consent event, which is not what we're measuring.
//
// Children of an elevated process inherit the Full token, so a Full child of a
// Full parent is not a new consent. parentKnown=false means the parent's token
// could not be inspected (parent exited, access denied): we favor counting,
// since the genuine UAC path re-parents the elevated process to a live,
// non-elevated requester (explorer.exe etc.), while an unknowable parent is
// usually a short-lived launcher.
func shouldCountElevation(procType, parentType uint32, parentKnown bool) bool {
	if procType != tokenElevationTypeFull {
		return false
	}
	if parentKnown && parentType == tokenElevationTypeFull {
		return false
	}
	return true
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
