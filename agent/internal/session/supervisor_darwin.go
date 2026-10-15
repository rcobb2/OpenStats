//go:build darwin

package session

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

type darwinPlatform struct {
	exe string
}

func newPlatform(*slog.Logger) (platform, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate own executable: %w", err)
	}
	return &darwinPlatform{exe: exe}, nil
}

// desired is the console user only. Fast-user-switched sessions in the
// background have no foreground the user is looking at, and the login window
// (owner uid 0) has no user to attribute time to.
func (p *darwinPlatform) desired() ([]target, error) {
	var st syscall.Stat_t
	if err := syscall.Stat("/dev/console", &st); err != nil {
		return nil, fmt.Errorf("stat /dev/console: %w", err)
	}
	if st.Uid == 0 {
		return nil, nil
	}
	return []target{{Key: "uid-" + strconv.Itoa(int(st.Uid)), ID: st.Uid}}, nil
}

func (p *darwinPlatform) spawn(t target) (*proc, error) {
	if os.Geteuid() != 0 {
		return nil, errNoPrivilege
	}
	uid := strconv.FormatUint(uint64(t.ID), 10)
	// `launchctl asuser` execs the command inside the user's bootstrap
	// namespace, the only way a daemon can reach that user's WindowServer.
	// The helper then drops from root to the user itself (--uid).
	cmd := exec.Command("/bin/launchctl", "asuser", uid, p.exe, "session-helper", "--uid", uid)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start launchctl: %w", err)
	}
	return &proc{
		Stdout: out,
		Kill:   func() { _ = cmd.Process.Kill() },
		// StdoutPipe must be fully read before Wait; watch() only calls Wait
		// after the read loop has ended.
		Wait: func() { _ = cmd.Wait() },
	}, nil
}

// validator accepts only PIDs owned by the helper's own user, so a helper
// (running as that user) cannot attribute time to someone else's process.
func (p *darwinPlatform) validator(t target) func(uint32) bool {
	return func(pid uint32) bool {
		kp, err := unix.SysctlKinfoProc("kern.proc.pid", int(pid))
		if err != nil {
			return false
		}
		return kp.Eproc.Ucred.Uid == t.ID
	}
}

func (p *darwinPlatform) close() {}
