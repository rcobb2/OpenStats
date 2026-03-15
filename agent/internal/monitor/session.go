package monitor

import (
	"log/slog"
	"sync"
	"time"
)

// UserSession represents a user login session on the device.
type UserSession struct {
	User      string
	SessionID uint32
	LoginTime time.Time
	Active    bool
}

// SessionTracker monitors user login/logout sessions.
type SessionTracker struct {
	mu       sync.RWMutex
	sessions map[uint32]*UserSession // SessionID -> session
	logger   *slog.Logger
}

// NewSessionTracker creates a new session tracker.
func NewSessionTracker(logger *slog.Logger) *SessionTracker {
	return &SessionTracker{
		sessions: make(map[uint32]*UserSession),
		logger:   logger,
	}
}

// OnLogin records a user login.
func (st *SessionTracker) OnLogin(user string, sessionID uint32) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.sessions[sessionID] = &UserSession{
		User:      user,
		SessionID: sessionID,
		LoginTime: time.Now(),
		Active:    true,
	}
	st.logger.Info("user logged in", "user", user, "sessionID", sessionID)
}

// OnLogoff records a user logoff and returns the completed session.
func (st *SessionTracker) OnLogoff(sessionID uint32) *UserSession {
	st.mu.Lock()
	defer st.mu.Unlock()

	session, ok := st.sessions[sessionID]
	if !ok {
		return nil
	}

	session.Active = false
	delete(st.sessions, sessionID)
	st.logger.Info("user logged off",
		"user", session.User,
		"sessionID", sessionID,
		"duration", time.Since(session.LoginTime),
	)
	return session
}

// ActiveSessions returns a snapshot of all active user sessions.
func (st *SessionTracker) ActiveSessions() []*UserSession {
	st.mu.RLock()
	defer st.mu.RUnlock()

	result := make([]*UserSession, 0, len(st.sessions))
	for _, s := range st.sessions {
		cp := *s
		result = append(result, &cp)
	}
	return result
}
