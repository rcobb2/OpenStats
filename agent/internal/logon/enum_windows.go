//go:build windows

package logon

import (
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	wtsapi32                  = syscall.MustLoadDLL("wtsapi32.dll")
	procWTSEnumerateSessionsW = wtsapi32.MustFindProc("WTSEnumerateSessionsW")
	procWTSQuerySessionInfoW  = wtsapi32.MustFindProc("WTSQuerySessionInformationW")
	procWTSFreeMemory         = wtsapi32.MustFindProc("WTSFreeMemory")
)

// wtsCurrentServerHandle is WTS_CURRENT_SERVER_HANDLE — the local machine.
const wtsCurrentServerHandle = uintptr(0)

// WTS_CONNECTSTATE_CLASS values we care about. A session sitting at the login
// screen reports no username, which Index drops.
const (
	wtsActive       = 0
	wtsConnected    = 1
	wtsDisconnected = 4
)

// WTS_INFO_CLASS values.
const (
	wtsUserName    = 5
	wtsDomainName  = 7
	wtsSessionInfo = 24
)

// wtsSessionInfoW mirrors WTS_SESSION_INFOW.
type wtsSessionInfoW struct {
	SessionID      uint32
	WinStationName *uint16
	State          uint32
}

// wtsInfoW mirrors the head of WTSINFOW, far enough to reach LogonTime.
// Layout per MSDN: State, SessionId, then six byte counters, then five
// LARGE_INTEGER times of which LogonTime is the fourth.
type wtsInfoW struct {
	State                   uint32
	SessionID               uint32
	IncomingBytes           uint32
	OutgoingBytes           uint32
	IncomingFrames          uint32
	OutgoingFrames          uint32
	IncomingCompressedBytes uint32
	OutgoingCompressedBytes uint32
	WinStationName          [32]uint16
	Domain                  [17]uint16
	UserName                [21]uint16
	ConnectTime             int64
	DisconnectTime          int64
	LastInputTime           int64
	LogonTime               int64
	CurrentTime             int64
}

// WindowsEnumerator lists logon sessions via the Windows Terminal Services API.
// It sees every signed-in session — console, RDP, and disconnected ones —
// independently of whether the user has any process the agent tracks.
type WindowsEnumerator struct{}

// NewEnumerator returns the platform logon enumerator.
func NewEnumerator() Enumerator { return &WindowsEnumerator{} }

func (e *WindowsEnumerator) Enumerate() ([]Session, error) {
	// The out-params stay typed pointers rather than uintptr: round-tripping an
	// OS allocation through uintptr hides it from the compiler's pointer rules.
	var first *wtsSessionInfoW
	var count uint32

	ret, _, err := procWTSEnumerateSessionsW.Call(
		wtsCurrentServerHandle,
		0, // Reserved, must be 0
		1, // Version, must be 1
		uintptr(unsafe.Pointer(&first)),
		uintptr(unsafe.Pointer(&count)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("WTSEnumerateSessions: %w", err)
	}
	if first == nil || count == 0 {
		return nil, nil
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(first)))

	// The API hands back a C array; walk it by element size.
	stride := unsafe.Sizeof(wtsSessionInfoW{})
	sessions := make([]Session, 0, count)
	for i := uint32(0); i < count; i++ {
		info := (*wtsSessionInfoW)(unsafe.Add(unsafe.Pointer(first), uintptr(i)*stride))

		state, ok := mapWTSState(info.State)
		if !ok {
			continue
		}

		user := queryWTSString(info.SessionID, wtsUserName)
		if user == "" {
			continue // login screen / unoccupied station
		}
		if domain := queryWTSString(info.SessionID, wtsDomainName); domain != "" {
			user = domain + `\` + user
		}

		sessions = append(sessions, Session{
			ID:        fmt.Sprintf("wts-%d", info.SessionID),
			RawUser:   user,
			State:     state,
			LoginTime: queryWTSLogonTime(info.SessionID),
		})
	}
	return sessions, nil
}

// mapWTSState keeps only states that mean "a user is signed in". Idle, listening
// and shadow stations are not occupancy.
func mapWTSState(state uint32) (State, bool) {
	switch state {
	case wtsActive:
		return StateActive, true
	case wtsConnected, wtsDisconnected:
		// Still signed in, just not attached to a display right now.
		return StateDisconnected, true
	default:
		return "", false
	}
}

// queryWTSString reads a string-valued session property.
func queryWTSString(sessionID uint32, infoClass uintptr) string {
	var buf *uint16
	var bytes uint32
	ret, _, _ := procWTSQuerySessionInfoW.Call(
		wtsCurrentServerHandle,
		uintptr(sessionID),
		infoClass,
		uintptr(unsafe.Pointer(&buf)),
		uintptr(unsafe.Pointer(&bytes)),
	)
	if ret == 0 || buf == nil {
		return ""
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(buf)))
	return strings.TrimSpace(utf16PtrToString(buf))
}

// queryWTSLogonTime reads the session's logon timestamp. Returns the zero time
// if unavailable, which the tracker treats as "started now".
func queryWTSLogonTime(sessionID uint32) time.Time {
	var info *wtsInfoW
	var bytes uint32
	ret, _, _ := procWTSQuerySessionInfoW.Call(
		wtsCurrentServerHandle,
		uintptr(sessionID),
		wtsSessionInfo,
		uintptr(unsafe.Pointer(&info)),
		uintptr(unsafe.Pointer(&bytes)),
	)
	if ret == 0 || info == nil {
		return time.Time{}
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(info)))

	if uintptr(bytes) < unsafe.Sizeof(wtsInfoW{}) || info.LogonTime == 0 {
		return time.Time{}
	}
	return time.Unix(0, filetimeToUnixNano(info.LogonTime))
}

// filetimeToUnixNano converts a Windows FILETIME (100ns ticks since 1601-01-01)
// to Unix nanoseconds.
func filetimeToUnixNano(ft int64) int64 {
	const ticksToUnixEpoch = 116444736000000000
	return (ft - ticksToUnixEpoch) * 100
}

// utf16PtrToString reads a NUL-terminated UTF-16 string.
func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	// The length cap is defensive: a malformed buffer must not walk memory
	// without bound.
	const maxChars = 1024
	var chars []uint16
	for i := 0; i < maxChars; i++ {
		c := *(*uint16)(unsafe.Add(unsafe.Pointer(p), uintptr(i)*unsafe.Sizeof(uint16(0))))
		if c == 0 {
			break
		}
		chars = append(chars, c)
	}
	return syscall.UTF16ToString(chars)
}
