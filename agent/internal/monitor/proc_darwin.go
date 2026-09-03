//go:build darwin

package monitor

/*
#include <libproc.h>
#include <stdlib.h>
#include <string.h>

// getAllPIDCount returns the number of currently running processes.
static int getAllPIDCount() {
    return proc_listallpids(NULL, 0);
}

// getAllPIDs fills buf (of length bufLen ints) with running PIDs.
// Returns the number of PIDs written, or -1 on error.
static int getAllPIDs(int *buf, int bufLen) {
    return proc_listallpids(buf, bufLen * (int)sizeof(int));
}

// getBSDInfo fills info with bsdinfo for pid.
// Returns 1 on success, 0 on failure.
static int getBSDInfo(int pid, struct proc_bsdinfo *info) {
    int ret = proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, info, (int)sizeof(struct proc_bsdinfo));
    return ret == (int)sizeof(struct proc_bsdinfo) ? 1 : 0;
}

// getProcPath fills buf with the full executable path for pid.
// buf must be at least PROC_PIDPATHINFO_MAXSIZE bytes.
// Returns path length on success, 0 on failure.
static int getProcPath(int pid, char *buf) {
    return proc_pidpath(pid, buf, PROC_PIDPATHINFO_MAXSIZE);
}
*/
import "C"

import (
	"context"
	"fmt"
	"log/slog"
	"os/user"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// procSnapshot captures what we know about a PID at observation time.
type procSnapshot struct {
	exeName   string
	exePath   string
	user      string
	ppid      uint32
	uid       uint32
	startSec  uint64 // pbi_start_tvsec from proc_bsdinfo
	startTime time.Time
}

// PollWatcher polls the macOS process list every second to detect launches and exits.
// It uses libproc (proc_listallpids, proc_pidinfo, proc_pidpath) via CGo.
type PollWatcher struct {
	tracker         *Tracker
	logger          *slog.Logger
	excludePatterns []*regexp.Regexp
	minLifetime     time.Duration
	familyResolver  func(exeName, exePath string) string
	onStart         func(pid uint32, exeName string, isNewGroup bool)
	onStop          func(session *ProcessSession)
	onElevated      func(pid uint32, exeName, exePath, user string)
	prevPIDs        map[uint32]procSnapshot
}

// NewPollWatcher creates a new macOS process watcher.
func NewPollWatcher(tracker *Tracker, logger *slog.Logger, cfg WMIWatcherConfig) (*PollWatcher, error) {
	patterns := make([]*regexp.Regexp, 0, len(cfg.ExcludePatterns))
	for _, p := range cfg.ExcludePatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid exclude pattern %q: %w", p, err)
		}
		patterns = append(patterns, re)
	}

	return &PollWatcher{
		tracker:         tracker,
		logger:          logger,
		excludePatterns: patterns,
		minLifetime:     cfg.MinLifetime,
		familyResolver:  cfg.FamilyResolver,
		onStart:         cfg.OnStart,
		onStop:          cfg.OnStop,
		onElevated:      cfg.OnElevated,
		prevPIDs:        make(map[uint32]procSnapshot),
	}, nil
}

// isExcluded checks if a process should be skipped.
// Returns true for:
//   - names matching any configured excludePatterns regex, or
//   - processes running from macOS system paths (not user-installed software).
func (w *PollWatcher) isExcluded(exeName, exePath string) bool {
	for _, re := range w.excludePatterns {
		if re.MatchString(exeName) {
			return true
		}
	}
	return isSystemPath(exePath)
}

// isSystemPath returns true when a process lives in a macOS system directory.
// These are OS daemons, XPC services, and private framework helpers — never
// user-installed lab software. /usr/local/ is intentionally excluded from this
// list because that's where Homebrew and user-installed CLI tools live.
func isSystemPath(exePath string) bool {
	for _, prefix := range []string{
		"/System/",
		"/usr/sbin/",
		"/usr/libexec/",
		"/usr/bin/",
		"/usr/lib/",
		"/sbin/",
		"/bin/",
		"/Library/Apple/",
	} {
		if len(exePath) > len(prefix) && exePath[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// FilterProcesses removes processes that would be excluded by this watcher's
// patterns. Used to filter the results of ScanExistingProcesses at startup.
func (w *PollWatcher) FilterProcesses(procs []RunningProcess) []RunningProcess {
	filtered := make([]RunningProcess, 0, len(procs))
	for _, p := range procs {
		if !w.isExcluded(p.ExeName, p.ExePath) {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// Run polls the process list every second until ctx is cancelled.
func (w *PollWatcher) Run(ctx context.Context) error {
	w.logger.Info("macOS process watcher starting (libproc polling)")

	// Do initial snapshot to populate prevPIDs without firing OnStart for
	// pre-existing processes (those are handled by ScanExistingProcesses).
	// detectElevations=false: a process already root when the agent starts is
	// adopted, not counted — an agent restart must not manufacture elevations.
	w.prevPIDs = w.currentSnapshot(false)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("macOS process watcher shutting down")
			return nil
		case <-ticker.C:
			w.poll()
		}
	}
}

// poll diffs the current PID list against the previous snapshot and fires callbacks.
func (w *PollWatcher) poll() {
	current := w.currentSnapshot(true)
	now := time.Now()

	// Detect new PIDs and PID reuse (same PID, different startSec = new process).
	for pid, snap := range current {
		prev, seen := w.prevPIDs[pid]
		if seen && snap.startSec == prev.startSec {
			continue // same process, no action needed
		}
		if w.isExcluded(snap.exeName, snap.exePath) {
			continue
		}
		// PID was reused: stop the old tracked process before starting the new one.
		if seen {
			if session := w.tracker.OnProcessStop(pid); session != nil && w.onStop != nil {
				w.onStop(session)
			}
		}
		var familyKey string
		if w.familyResolver != nil {
			familyKey = w.familyResolver(snap.exeName, snap.exePath)
		}
		isNewGroup := w.tracker.OnProcessStart(pid, snap.ppid, snap.exeName, snap.exePath, snap.user, familyKey)
		if w.onStart != nil {
			w.onStart(pid, snap.exeName, isNewGroup)
		}
	}

	// Detect gone PIDs.
	for pid, snap := range w.prevPIDs {
		if _, still := current[pid]; !still {
			// Apply minLifetime filter.
			lifetime := now.Sub(snap.startTime)
			if w.minLifetime > 0 && lifetime < w.minLifetime {
				w.tracker.OnProcessStop(pid) // remove from tracker without firing onStop
				continue
			}

			session := w.tracker.OnProcessStop(pid)
			if session != nil && w.onStop != nil {
				w.onStop(session)
			}
		}
	}

	w.prevPIDs = current
}

// listAllPIDsFn and getProcBSDInfoFn indirect the real libproc calls so tests
// can simulate a root-owned process (e.g. a sudo child) flowing through the
// real, unmodified currentSnapshot()/poll() pipeline without requiring actual
// root privileges — genuinely elevating a test process isn't possible without
// an interactive password. Production code always uses the real functions;
// only tests override these.
var (
	listAllPIDsFn    = listAllPIDs
	getProcBSDInfoFn = getProcBSDInfo
)

// currentSnapshot returns a map of all running PIDs and their info.
// detectElevations must be false for the one-time startup seed (Run() calling
// this before prevPIDs exists, where every already-running process would
// otherwise look "new") and true for every subsequent poll tick.
func (w *PollWatcher) currentSnapshot(detectElevations bool) map[uint32]procSnapshot {
	pids := listAllPIDsFn()
	snap := make(map[uint32]procSnapshot, len(pids))

	// root0Seen is a cheap field-diagnostic signal: proc_pidinfo requires the
	// caller to own the target process or be root (see
	// TestGetProcBSDInfoCrossUserRequiresRoot), so root0Seen==0 with a
	// nonzero infoFailures count means this agent isn't running privileged
	// enough to see other users' processes at all — elevation detection (and
	// usage tracking for other users) will silently see nothing.
	var infoFailures, root0Seen int

	for _, pid := range pids {
		if pid == 0 {
			continue
		}
		info, ok := getProcBSDInfoFn(pid)
		if !ok {
			infoFailures++
			continue
		}
		if info.uid == 0 {
			root0Seen++
		}
		exeName := info.exeName
		if exeName == "" {
			continue
		}
		exePath := getExePath(pid)
		pathUnreadable := exePath == ""
		if pathUnreadable {
			exePath = exeName
		}

		// Field-diagnostic breadcrumb: if this never fires despite real
		// elevation activity on a machine, it means the agent isn't running
		// privileged enough to see root-owned processes at all (see
		// root0Seen above). Unconditional — not gated by detectElevations —
		// so it also fires during the startup seed; cheap, since uid-0
		// processes are a small fraction of the process table.
		if info.uid == 0 {
			w.logger.Debug("observed root-owned process", "pid", pid, "exe", exeName, "ppid", info.ppid, "pathUnreadable", pathUnreadable)
		}

		// Elevation detection runs before the exclude/system-path filtering
		// below, and before the "unreadable root path → system daemon" skip.
		// The most common escalation targets — sudo'd /usr/sbin, /usr/bin
		// utilities (softwareupdate, launchctl, installer...) — are exactly
		// what those filters treat as noise for usage tracking. They are not
		// noise for elevation tracking; they're the whole point.
		if detectElevations && w.onElevated != nil {
			prev, seen := w.prevPIDs[pid]
			isNew := !seen || info.startSec != prev.startSec
			if isNew && info.uid == 0 {
				invokingUser, ok := rootEscalationInvoker(info.uid, info.ppid)
				w.logger.Debug("evaluated possible elevation", "pid", pid, "exe", exeName, "ppid", info.ppid, "counted", ok, "invokingUser", invokingUser)
				if ok {
					w.onElevated(pid, exeName, exePath, invokingUser)
				}
			}
		}

		// When proc_pidpath can't read the path (e.g., root-owned daemon and we
		// are running as a regular user), fall back to the exe name for pattern
		// matching but treat a missing path on a root process as a system daemon.
		if pathUnreadable && info.uid == 0 {
			continue // root process we can't path-read → system daemon, skip
		}
		if w.isExcluded(exeName, exePath) {
			continue
		}
		username := resolveUID(info.uid)
		snap[pid] = procSnapshot{
			exeName:   exeName,
			exePath:   exePath,
			user:      username,
			ppid:      info.ppid,
			uid:       info.uid,
			startSec:  info.startSec,
			startTime: time.Unix(int64(info.startSec), 0),
		}
	}
	w.logger.Debug("snapshot pass complete", "totalPIDs", len(pids), "infoFailures", infoFailures, "root0Seen", root0Seen)
	return snap
}

// procBSDInfo is a trimmed version of struct proc_bsdinfo.
type procBSDInfo struct {
	exeName  string
	ppid     uint32
	uid      uint32
	startSec uint64
}

// getProcBSDInfo calls proc_pidinfo(PROC_PIDTBSDINFO) for a single process.
func getProcBSDInfo(pid uint32) (procBSDInfo, bool) {
	var info C.struct_proc_bsdinfo
	if C.getBSDInfo(C.int(pid), &info) == 0 {
		return procBSDInfo{}, false
	}

	// pbi_name is the longer name (up to 2*MAXCOMLEN = 32 chars), preferred over pbi_comm.
	name := C.GoString(&info.pbi_name[0])
	if name == "" {
		name = C.GoString(&info.pbi_comm[0])
	}

	return procBSDInfo{
		exeName:  name,
		ppid:     uint32(info.pbi_ppid),
		uid:      uint32(info.pbi_uid),
		startSec: uint64(info.pbi_start_tvsec),
	}, true
}

// getExePath returns the full executable path for a PID using proc_pidpath.
func getExePath(pid uint32) string {
	buf := make([]C.char, C.PROC_PIDPATHINFO_MAXSIZE)
	ret := C.getProcPath(C.int(pid), &buf[0])
	if ret <= 0 {
		return ""
	}
	return C.GoString(&buf[0])
}

// listAllPIDs returns the PIDs of all running processes.
func listAllPIDs() []uint32 {
	// First call: get count.
	count := int(C.getAllPIDCount())
	if count <= 0 {
		return nil
	}

	// Allocate with some extra headroom for new processes between calls.
	count += 16
	buf := make([]C.int, count)
	n := int(C.getAllPIDs(&buf[0], C.int(count)))
	if n <= 0 {
		return nil
	}

	pids := make([]uint32, n)
	for i := 0; i < n; i++ {
		pids[i] = uint32(buf[i])
	}
	return pids
}

var (
	uidCacheMu sync.RWMutex
	uidCache   = make(map[uint32]string)
)

// resolveUID converts a UID to a username, caching results to avoid repeated syscalls.
func resolveUID(uid uint32) string {
	uidCacheMu.RLock()
	if name, ok := uidCache[uid]; ok {
		uidCacheMu.RUnlock()
		return name
	}
	uidCacheMu.RUnlock()

	name := ""
	if u, err := user.LookupId(strconv.Itoa(int(uid))); err == nil {
		name = u.Username
	}

	uidCacheMu.Lock()
	uidCache[uid] = name
	uidCacheMu.Unlock()
	return name
}

// ScanExistingProcesses returns all currently running processes.
// Called at agent startup to register processes that started before the agent.
func ScanExistingProcesses(logger *slog.Logger, familyResolver func(string, string) string) []RunningProcess {
	pids := listAllPIDs()
	var result []RunningProcess

	for _, pid := range pids {
		if pid == 0 {
			continue
		}
		info, ok := getProcBSDInfo(pid)
		if !ok || info.exeName == "" {
			continue
		}
		exePath := getExePath(pid)
		if exePath == "" {
			if info.uid == 0 {
				continue // root daemon we can't path-read → skip
			}
			exePath = info.exeName
		}
		if isSystemPath(exePath) {
			continue
		}
		username := resolveUID(info.uid)

		var familyKey string
		if familyResolver != nil {
			familyKey = familyResolver(info.exeName, exePath)
		}

		result = append(result, RunningProcess{
			PID:       pid,
			ParentPID: info.ppid,
			ExeName:   info.exeName,
			ExePath:   exePath,
			User:      username,
			FamilyKey: familyKey,
		})
	}

	logger.Info("scanned existing processes", "count", len(result))
	return result
}
