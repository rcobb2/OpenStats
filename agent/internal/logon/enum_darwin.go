//go:build darwin

package logon

/*
#include <stdlib.h>
#include <utmpx.h>

// utmpx gives every current login record: the console session plus any SSH
// logins. There is no Cocoa API that reports all of them without a GUI session,
// and SCDynamicStoreCopyConsoleUser only ever reports the one console user.
*/
import "C"

import (
	"fmt"
	"strings"
	"time"
)

// DarwinEnumerator lists logon sessions from utmpx login records.
type DarwinEnumerator struct{}

// NewEnumerator returns the platform logon enumerator.
func NewEnumerator() Enumerator { return &DarwinEnumerator{} }

func (e *DarwinEnumerator) Enumerate() ([]Session, error) {
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

		user := strings.TrimSpace(C.GoString(&entry.ut_user[0]))
		if user == "" {
			continue
		}
		line := strings.TrimSpace(C.GoString(&entry.ut_line[0]))

		sessions = append(sessions, Session{
			// The tty/line distinguishes concurrent logins for one user; console
			// and ssh sessions must not collapse into one.
			ID:        fmt.Sprintf("utmpx-%s", line),
			RawUser:   user,
			State:     darwinState(line),
			LoginTime: time.Unix(int64(entry.ut_tv.tv_sec), 0),
		})
	}
	return sessions, nil
}

// darwinState marks the window-server console session as attached and remote or
// detached tty logins as disconnected. A user signed in over SSH occupies the
// machine differently from one sitting at it.
func darwinState(line string) State {
	if strings.HasPrefix(line, "console") {
		return StateActive
	}
	return StateDisconnected
}
