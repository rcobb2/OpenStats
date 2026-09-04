//go:build darwin

package logon

/*
#include <stdlib.h>
#include <string.h>
#include <utmpx.h>
#include <libproc.h>

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
*/
import "C"

import (
	"fmt"
	"os/user"
	"strings"
	"time"
)

// DarwinEnumerator lists logon sessions on macOS from two disjoint sources:
//
//   - One session per running loginwindow process, owned by whichever user it
//     belongs to. macOS keeps every Fast-User-Switched-in GUI user's full
//     session alive simultaneously, each running its own loginwindow instance
//     — this is the only signal that still sees a user switched into the
//     background. utmpx does not: it is effectively single-console, and in
//     practice only ever reflects whichever user is currently frontmost.
//   - utmpx login records for anything other than the console line (i.e. SSH)
//     — loginwindow has no equivalent for a remote shell session, so utmpx
//     remains the only source for those.
//
// A user signed in both at the console and over SSH still reports as two
// sessions, matching the console+ssh distinction this package has always made.
type DarwinEnumerator struct{}

// NewEnumerator returns the platform logon enumerator.
func NewEnumerator() Enumerator { return &DarwinEnumerator{} }

func (e *DarwinEnumerator) Enumerate() ([]Session, error) {
	sessions := enumerateLoginwindowSessions()
	sessions = append(sessions, enumerateSSHSessions()...)
	return sessions, nil
}

// enumerateLoginwindowSessions returns one session per running loginwindow
// process. Keying by PID means a session is stable across polls for as long
// as that FUS slot exists, and disappears the instant the user actually logs
// out and the process exits — exactly the start/end transition the tracker
// needs, without an extra "which one is frontmost" API call.
//
// LoginTime comes from proc_bsdinfo's pbi_start_tvsec, which is not trusted
// blindly: loginwindow launches very early in boot, before the clock has
// synced to NTP/RTC, and its recorded start time stays frozen to that
// pre-sync reading for the process's entire lifetime even after the kernel's
// own boot-time estimate is later corrected (observed on a real, otherwise
// healthy Mac as a start time of 1976). The tracker's maxPlausibleSessionAge
// guard catches this the same way it catches a bad utmpx record, so an
// implausible value here is not filtered specially — it is passed through
// and left to that shared guard.
func enumerateLoginwindowSessions() []Session {
	count := int(C.getAllPIDCount())
	if count <= 0 {
		return nil
	}
	count += 16 // headroom for new processes between the two calls
	buf := make([]C.int, count)
	n := int(C.getAllPIDs(&buf[0], C.int(count)))
	if n <= 0 {
		return nil
	}

	var sessions []Session
	for i := 0; i < n; i++ {
		pid := int(buf[i])
		if pid <= 0 {
			continue
		}
		var info C.struct_proc_bsdinfo
		if C.getBSDInfo(C.int(pid), &info) != 1 {
			continue // exited between listing and query, or inaccessible
		}
		if C.GoString(&info.pbi_name[0]) != "loginwindow" {
			continue
		}

		u, err := user.LookupId(fmt.Sprint(uint32(info.pbi_uid)))
		if err != nil || u.Username == "" {
			continue
		}

		sessions = append(sessions, Session{
			ID:      fmt.Sprintf("loginwindow-%d", pid),
			RawUser: u.Username,
			State:   StateActive,
			LoginTime: time.Unix(
				int64(info.pbi_start_tvsec), int64(info.pbi_start_tvusec)*1000),
		})
	}
	return sessions
}

// enumerateSSHSessions returns utmpx login records for anything other than
// the console line. The console record is skipped deliberately:
// enumerateLoginwindowSessions is its FUS-aware replacement.
func enumerateSSHSessions() []Session {
	// setutxent/getutxent/endutxent share process-global state, so this must not
	// run concurrently with itself. The tracker polls from a single goroutine.
	C.setutxent()
	defer C.endutxent()

	var sessions []Session
	for {
		entry := C.getutxent()
		if entry == nil {
			break
		}
		// Only USER_PROCESS records are live logins. BOOT_TIME, DEAD_PROCESS and
		// the rest are history or bookkeeping.
		if entry.ut_type != C.USER_PROCESS {
			continue
		}

		line := strings.TrimSpace(C.GoString(&entry.ut_line[0]))
		if strings.HasPrefix(line, "console") {
			continue // covered by enumerateLoginwindowSessions instead
		}

		rawUser := strings.TrimSpace(C.GoString(&entry.ut_user[0]))
		if rawUser == "" {
			continue
		}

		sessions = append(sessions, Session{
			ID:        fmt.Sprintf("utmpx-%s", line),
			RawUser:   rawUser,
			State:     StateDisconnected,
			LoginTime: time.Unix(int64(entry.ut_tv.tv_sec), 0),
		})
	}
	return sessions
}
