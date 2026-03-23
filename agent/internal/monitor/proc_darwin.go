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
	"time"
)

// procSnapshot captures what we know about a PID at observation time.
type procSnapshot struct {
	exeName   string
	exePath   string
	user      string
	ppid      uint32
	uid       uint32
	startSec  uint64  // pbi_start_tvsec from proc_bsdinfo
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
// user-installed lab software.
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
	w.prevPIDs = w.currentSnapshot()

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
	current := w.currentSnapshot()
	now := time.Now()

	// Detect new PIDs.
	for pid, snap := range current {
		if _, seen := w.prevPIDs[pid]; !seen {
			if w.isExcluded(snap.exeName, snap.exePath) {
				continue
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

// currentSnapshot returns a map of all running PIDs and their info.
func (w *PollWatcher) currentSnapshot() map[uint32]procSnapshot {
	pids := listAllPIDs()
	snap := make(map[uint32]procSnapshot, len(pids))

	for _, pid := range pids {
		if pid == 0 {
			continue
		}
		info, ok := getProcBSDInfo(pid)
		if !ok {
			continue
		}
		exeName := info.exeName
		if exeName == "" {
			continue
		}
		exePath := getExePath(pid)
		// When proc_pidpath can't read the path (e.g., root-owned daemon and we
		// are running as a regular user), fall back to the exe name for pattern
		// matching but treat a missing path on a root process as a system daemon.
		if exePath == "" {
			if info.uid == 0 {
				continue // root process we can't path-read → system daemon, skip
			}
			exePath = exeName
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
	return snap
}

// procBSDInfo is a trimmed version of struct proc_bsdinfo.
type procBSDInfo struct {
	exeName string
	ppid    uint32
	uid     uint32
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

// resolveUID converts a UID to a username using the standard library.
func resolveUID(uid uint32) string {
	u, err := user.LookupId(strconv.Itoa(int(uid)))
	if err != nil {
		return ""
	}
	return u.Username
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
