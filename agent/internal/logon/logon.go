// Package logon tracks operating-system logon sessions.
//
// Session metrics used to be derived from process start/stop refcounts: the
// first tracked process for a user counted as a login, and the last one exiting
// counted as a logoff. That proxy breaks down on machines that are never signed
// out — a kiosk account or a service account holds the refcount above zero
// forever, so exactly one login is ever recorded and reports built on
// increase(logins_total[...]) stay empty. It also re-counted every open session
// as a fresh login whenever the agent restarted.
//
// This package asks the OS instead: WTS session enumeration on Windows, utmpx
// login records on macOS. A logon is a logon whether or not the user happens to
// have a tracked process running, and a restart of the agent no longer
// manufactures one.
package logon

import (
	"sort"
	"strings"
	"time"
)

// State is the connection state of a logon session. A session can be signed in
// but not currently attached to a display (a disconnected RDP session, or a
// macOS session behind the login window), which still occupies the machine from
// a licensing and utilization standpoint.
type State string

const (
	// StateActive means the session is signed in and attached.
	StateActive State = "active"
	// StateDisconnected means the user is still signed in but detached.
	StateDisconnected State = "disconnected"
)

// Session is one OS logon session.
type Session struct {
	// ID distinguishes concurrent sessions on one machine (Windows WTS session
	// ID, or the macOS utmpx line). Two sessions for the same user — fast user
	// switching, or a console login plus an SSH login — must not collapse.
	ID string
	// RawUser is the username exactly as the OS reports it, including any domain
	// qualifier. Canonicalization happens in the caller so that one policy
	// applies to every username the agent handles.
	RawUser string
	State   State
	// LoginTime is when the OS says the session began. Zero if unavailable, in
	// which case the tracker substitutes first-seen time.
	LoginTime time.Time
}

// Key identifies a session for diffing purposes.
func (s Session) Key() SessionKey {
	return SessionKey{ID: s.ID, RawUser: strings.ToLower(strings.TrimSpace(s.RawUser))}
}

// SessionKey is the identity of a session across polls.
type SessionKey struct {
	ID      string
	RawUser string
}

// Enumerator returns the OS logon sessions currently present. Implementations
// are platform-specific and must not block for long — the poller calls this on
// a fixed interval.
type Enumerator interface {
	Enumerate() ([]Session, error)
}

// Diff reports which sessions began and which ended between two polls.
// Sessions present in both are returned as `continuing`, so the caller can
// refresh duration gauges without re-counting a logon.
//
// A session whose state changed (attached ↔ detached) is continuing, not a new
// logon: the user never signed out.
func Diff(previous, current map[SessionKey]Session) (started, ended, continuing []Session) {
	for key, cur := range current {
		if _, existed := previous[key]; existed {
			continuing = append(continuing, cur)
			continue
		}
		started = append(started, cur)
	}
	for key, prev := range previous {
		if _, stillPresent := current[key]; !stillPresent {
			ended = append(ended, prev)
		}
	}
	sortSessions(started)
	sortSessions(ended)
	sortSessions(continuing)
	return started, ended, continuing
}

// Index keys a session slice for diffing, dropping entries with no username
// (an unoccupied console or a login screen is not a session).
func Index(sessions []Session) map[SessionKey]Session {
	out := make(map[SessionKey]Session, len(sessions))
	for _, s := range sessions {
		if strings.TrimSpace(s.RawUser) == "" {
			continue
		}
		out[s.Key()] = s
	}
	return out
}

// sortSessions gives deterministic ordering, which keeps log output and tests
// stable regardless of map iteration order.
func sortSessions(sessions []Session) {
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].RawUser != sessions[j].RawUser {
			return sessions[i].RawUser < sessions[j].RawUser
		}
		return sessions[i].ID < sessions[j].ID
	})
}
