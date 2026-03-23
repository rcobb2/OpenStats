//go:build darwin

package monitor

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>

// getForegroundPIDCGO returns the PID of the process owning the frontmost
// on-screen window at layer 0 (the normal application layer).
//
// Windows are returned front-to-back by CGWindowListCopyWindowInfo, so the
// first layer-0 window belongs to the active application.
//
// Returns 0 when:
//   - Screen Recording permission has not been granted (TCC), in which case
//     the OS returns only windows owned by this process (none for a daemon).
//   - No display is connected (headless / SSH-only session).
//   - No suitable window is found.
static pid_t getForegroundPIDCGO() {
    CFArrayRef list = CGWindowListCopyWindowInfo(
        kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements,
        kCGNullWindowID);
    if (!list) return 0;

    pid_t result = 0;
    CFIndex count = CFArrayGetCount(list);

    for (CFIndex i = 0; i < count; i++) {
        CFDictionaryRef win = (CFDictionaryRef)CFArrayGetValueAtIndex(list, i);
        if (!win) continue;

        // Only consider normal application-layer windows (layer == 0).
        // Higher layers are menu bar items, screensavers, overlays, etc.
        CFNumberRef layerRef = (CFNumberRef)CFDictionaryGetValue(win, kCGWindowLayer);
        if (!layerRef) continue;
        int32_t layer = 0;
        if (!CFNumberGetValue(layerRef, kCFNumberSInt32Type, &layer)) continue;
        if (layer != 0) continue;

        CFNumberRef pidRef = (CFNumberRef)CFDictionaryGetValue(win, kCGWindowOwnerPID);
        if (!pidRef) continue;
        int32_t pid = 0;
        if (!CFNumberGetValue(pidRef, kCFNumberSInt32Type, &pid)) continue;
        if (pid > 0) {
            result = (pid_t)pid;
            break; // front-to-back order: first match is the active app
        }
    }

    CFRelease(list);
    return result;
}
*/
import "C"

// getForegroundPID returns the PID of the frontmost application window.
// Returns 0 when Screen Recording permission is not granted or no display
// session is active. RunForegroundPoller treats 0 as "no active window" and
// skips the increment — all other metrics continue to work normally.
func getForegroundPID() uint32 {
	return uint32(C.getForegroundPIDCGO())
}
