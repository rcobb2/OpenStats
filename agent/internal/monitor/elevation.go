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
