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

// shouldCountRootLaunch decides whether a just-started macOS process
// represents a genuine privilege escalation (sudo, an admin AppleScript, an
// installer's root helper). Only a process now running as uid 0 with a live,
// non-root parent counts — a root process forked by another root process (a
// daemon's own child, launchd's tree) is not a new escalation, it's just root
// software running.
//
// Unlike the Windows check, parentKnown=false always means "do not count".
// A UAC split token keeps the original user's identity even once elevated, so
// Windows can attribute an elevation regardless of the parent. sudo fully
// changes the process owner to root, so on macOS the elevated process itself
// can never be attributed to a human — attribution has to come from the
// parent's owner. Without a readable parent there is no one to attribute the
// event to, so it isn't counted.
func shouldCountRootLaunch(childUID, parentUID uint32, parentKnown bool) bool {
	if childUID != 0 {
		return false
	}
	return parentKnown && parentUID != 0
}
