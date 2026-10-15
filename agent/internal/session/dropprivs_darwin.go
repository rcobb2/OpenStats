//go:build darwin

package session

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// DropToUID makes a root helper run as uid before it touches WindowServer,
// which rejects connections from a uid that is not the console user. It is a
// no-op when not root. Any failure is returned and the caller must abort: a
// helper that keeps root would be sampling the wrong session and is a needless
// privileged process.
func DropToUID(uid int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	u, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return fmt.Errorf("look up uid %d: %w", uid, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return fmt.Errorf("parse gid %q: %w", u.Gid, err)
	}
	// Order matters: once setuid succeeds we no longer have the right to
	// change groups.
	if err := syscall.Setgroups([]int{gid}); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("setgid: %w", err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("setuid: %w", err)
	}
	if os.Geteuid() == 0 || os.Getuid() == 0 {
		return fmt.Errorf("still root after dropping privileges")
	}
	return nil
}
