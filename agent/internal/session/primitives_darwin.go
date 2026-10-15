//go:build darwin

package session

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -framework IOKit
#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>
#include <string.h>

// getForegroundPIDCGO returns the PID of the process owning the frontmost
// on-screen window at layer 0 (the normal application layer).
//
// Windows are returned front-to-back by CGWindowListCopyWindowInfo, so the
// first layer-0 window belongs to the active application. Owner PID and layer
// are available without Screen Recording permission (only titles/contents
// need it), but the call only sees the real desktop from inside the console
// user's GUI session.
//
// Returns 0 when no display is connected or no suitable window is found.
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

// hidIdleNanosCGO returns nanoseconds since the last HID (keyboard/mouse)
// event from the IOHIDSystem registry entry, or -1 if unavailable. The
// property is a CFNumber on current systems but has been published as CFData
// on others, so both forms are handled.
static int64_t hidIdleNanosCGO() {
    // Port 0 (MACH_PORT_NULL) selects the default main port without naming
    // kIOMasterPortDefault / kIOMainPortDefault, which differ by OS version.
    io_iterator_t it = 0;
    if (IOServiceGetMatchingServices(0, IOServiceMatching("IOHIDSystem"), &it) != KERN_SUCCESS) {
        return -1;
    }
    int64_t result = -1;
    io_registry_entry_t entry = IOIteratorNext(it);
    if (entry) {
        CFTypeRef prop = IORegistryEntryCreateCFProperty(entry, CFSTR("HIDIdleTime"), kCFAllocatorDefault, 0);
        if (prop) {
            if (CFGetTypeID(prop) == CFNumberGetTypeID()) {
                int64_t v = 0;
                if (CFNumberGetValue((CFNumberRef)prop, kCFNumberSInt64Type, &v)) result = v;
            } else if (CFGetTypeID(prop) == CFDataGetTypeID()) {
                CFDataRef d = (CFDataRef)prop;
                if (CFDataGetLength(d) >= (CFIndex)sizeof(int64_t)) {
                    int64_t v = 0;
                    memcpy(&v, CFDataGetBytePtr(d), sizeof(v));
                    result = v;
                }
            }
            CFRelease(prop);
        }
        IOObjectRelease(entry);
    }
    IOObjectRelease(it);
    return result;
}
*/
import "C"

import "time"

// ForegroundPID returns the PID of the frontmost application window, or 0 if
// none is visible. Only meaningful inside the console user's GUI session.
func ForegroundPID() uint32 {
	return uint32(C.getForegroundPIDCGO())
}

// IdleTime returns the time since the last keyboard/mouse input, or 0 if it
// cannot be determined (treated by the caller as "not idle").
func IdleTime() time.Duration {
	ns := int64(C.hidIdleNanosCGO())
	if ns < 0 {
		return 0
	}
	return time.Duration(ns)
}
