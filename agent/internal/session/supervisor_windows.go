//go:build windows

package session

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	wtsapi32                 = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSQuerySessionInfoW = wtsapi32.NewProc("WTSQuerySessionInformationW")
)

// WTS_INFO_CLASS WTSUserName.
const wtsUserName = 5

type windowsPlatform struct {
	exe string
	// job has KILL_ON_JOB_CLOSE, so every helper dies when the service
	// process exits for any reason (stop, crash, MSI upgrade). Without it a
	// surviving helper would keep openlabstats-agent.exe locked and break the
	// installer's file replacement.
	job windows.Handle
}

func newPlatform(*slog.Logger) (platform, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate own executable: %w", err)
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	return &windowsPlatform{exe: exe, job: job}, nil
}

func (p *windowsPlatform) close() {
	// Closing the last job handle kills any helper still alive.
	windows.CloseHandle(p.job)
}

// desired is every active session with a signed-in user. Disconnected RDP
// sessions have no visible desktop, and the login screen has no user.
func (p *windowsPlatform) desired() ([]target, error) {
	var first *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &first, &count); err != nil {
		return nil, fmt.Errorf("WTSEnumerateSessions: %w", err)
	}
	if first == nil || count == 0 {
		return nil, nil
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(first)))

	sessions := unsafe.Slice(first, count)
	var out []target
	for _, s := range sessions {
		if s.State != windows.WTSActive || s.SessionID == 0 {
			continue
		}
		if sessionUserName(s.SessionID) == "" {
			continue
		}
		out = append(out, target{Key: "session-" + strconv.FormatUint(uint64(s.SessionID), 10), ID: s.SessionID})
	}
	return out, nil
}

func sessionUserName(id uint32) string {
	var buf *uint16
	var n uint32
	r, _, _ := procWTSQuerySessionInfoW.Call(0, uintptr(id), wtsUserName,
		uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&n)))
	if r == 0 || buf == nil {
		return ""
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buf)))
	return windows.UTF16PtrToString(buf)
}

// validator accepts only PIDs that live in the helper's own session, so a
// helper cannot claim time for a process in another user's session.
func (p *windowsPlatform) validator(t target) func(uint32) bool {
	return func(pid uint32) bool {
		var sid uint32
		if err := windows.ProcessIdToSessionId(pid, &sid); err != nil {
			return false
		}
		return sid == t.ID
	}
}

// winProc guards the process handle so Kill can never race Wait's CloseHandle
// into signalling a recycled handle value.
type winProc struct {
	mu     sync.Mutex
	handle windows.Handle
	closed bool
}

func (w *winProc) kill() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		_ = windows.TerminateProcess(w.handle, 1)
	}
}

func (w *winProc) wait() {
	_, _ = windows.WaitForSingleObject(w.handle, windows.INFINITE)
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.closed = true
		windows.CloseHandle(w.handle)
	}
}

// spawn starts the helper inside the target session as that session's user.
//
// stdout is an anonymous pipe created here and inherited only by the helper:
// anything reading the pipe is by construction the process we launched into
// that session, so a sample's origin cannot be forged by another local process
// the way a named pipe or TCP port could.
func (p *windowsPlatform) spawn(t target) (*proc, error) {
	var token windows.Token
	if err := windows.WTSQueryUserToken(t.ID, &token); err != nil {
		if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
			return nil, fmt.Errorf("%w: WTSQueryUserToken: %v", errNoPrivilege, err)
		}
		return nil, fmt.Errorf("WTSQueryUserToken(session %d): %w", t.ID, err)
	}
	defer token.Close()

	var env *uint16
	if err := windows.CreateEnvironmentBlock(&env, token, false); err != nil {
		return nil, fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}
	defer windows.DestroyEnvironmentBlock(env)

	// Both pipe ends start inheritable; the read end is then made
	// non-inheritable. The handle list below is what actually restricts
	// inheritance, this is belt and braces.
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	var rd, wr windows.Handle
	if err := windows.CreatePipe(&rd, &wr, sa, 0); err != nil {
		return nil, fmt.Errorf("CreatePipe: %w", err)
	}
	// The parent's write end is closed on every path once the child holds
	// its own copy (or the spawn failed).
	defer windows.CloseHandle(wr)
	started := false
	defer func() {
		if !started {
			windows.CloseHandle(rd)
		}
	}()
	if err := windows.SetHandleInformation(rd, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		return nil, fmt.Errorf("SetHandleInformation: %w", err)
	}

	nul, err := windows.CreateFile(windows.StringToUTF16Ptr("NUL"), windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, sa, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("open NUL: %w", err)
	}
	defer windows.CloseHandle(nul)

	// Only these two handles are inherited, so the helper can't pick up any
	// other inheritable handle that happens to be open in the service.
	handles := []windows.Handle{nul, wr}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, fmt.Errorf("NewProcThreadAttributeList: %w", err)
	}
	defer attrs.Delete()
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]),
		uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return nil, fmt.Errorf("UpdateProcThreadAttribute: %w", err)
	}

	var si windows.StartupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.Desktop = windows.StringToUTF16Ptr(`winsta0\default`)
	si.StdInput, si.StdOutput, si.StdErr = nul, wr, wr
	si.ProcThreadAttributeList = attrs.List()

	appName, err := windows.UTF16PtrFromString(p.exe)
	if err != nil {
		return nil, err
	}
	// CreateProcessAsUser may write into the command line buffer, so it gets
	// its own fresh copy.
	cmdline, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{p.exe, "session-helper"}))
	if err != nil {
		return nil, err
	}

	// Suspended so the process is in the kill-on-close job before it runs a
	// single instruction.
	flags := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT |
		windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_SUSPENDED)
	var pi windows.ProcessInformation
	if err := windows.CreateProcessAsUser(token, appName, cmdline, nil, nil, true, flags, env, nil, &si.StartupInfo, &pi); err != nil {
		return nil, fmt.Errorf("CreateProcessAsUser: %w", err)
	}
	if err := windows.AssignProcessToJobObject(p.job, pi.Process); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(pi.Thread)
		windows.CloseHandle(pi.Process)
		return nil, fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(pi.Thread)
		windows.CloseHandle(pi.Process)
		return nil, fmt.Errorf("ResumeThread: %w", err)
	}
	windows.CloseHandle(pi.Thread)

	started = true
	wp := &winProc{handle: pi.Process}
	return &proc{
		Stdout: os.NewFile(uintptr(rd), "session-helper-stdout"),
		Kill:   wp.kill,
		Wait:   wp.wait,
	}, nil
}
