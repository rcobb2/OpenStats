package logon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/rcobb/openlabstats-agent/internal/metrics"
)

// ResolveFunc canonicalizes a raw OS username and reports whether it should be
// tracked at all. It is supplied by the caller so that logon sessions go through
// the same ignore/correlation policy as every other username the agent handles.
type ResolveFunc func(rawUser string) (canonical string, ok bool)

// Tracker converts OS logon sessions into the user-session metrics. It owns
// those four metrics exclusively — nothing else may write them, or a user's
// session would be counted twice.
type Tracker struct {
	mu       sync.Mutex
	enum     Enumerator
	metrics  *metrics.Metrics
	resolve  ResolveFunc
	hostname string
	logger   *slog.Logger

	// seeded is false until the first successful poll. The sessions present at
	// startup are adopted rather than counted as logons: they began before this
	// process did, and counting them is what made every login counter read
	// exactly 1 after an agent restart.
	seeded   bool
	sessions map[SessionKey]Session
	users    map[string]*userState
}

// userState aggregates every OS session belonging to one canonical user on this
// machine. A user with a console session and an SSH session is one occupant.
type userState struct {
	sessions      int
	earliestLogin time.Time
	lastAccrual   time.Time
}

// NewTracker creates a logon tracker. resolve may be nil, in which case every
// non-empty username is tracked as-is.
func NewTracker(enum Enumerator, m *metrics.Metrics, resolve ResolveFunc, hostname string, logger *slog.Logger) *Tracker {
	if resolve == nil {
		resolve = func(raw string) (string, bool) { return raw, raw != "" }
	}
	return &Tracker{
		enum:     enum,
		metrics:  m,
		resolve:  resolve,
		hostname: hostname,
		logger:   logger,
		sessions: make(map[SessionKey]Session),
		users:    make(map[string]*userState),
	}
}

// Run polls until ctx is cancelled. The first poll happens immediately so that
// active sessions appear without waiting out the interval.
func (t *Tracker) Run(ctx context.Context, interval time.Duration) {
	if err := t.Poll(); err != nil {
		t.logger.Warn("logon enumeration failed", "error", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := t.Poll(); err != nil {
				t.logger.Warn("logon enumeration failed", "error", err)
			}
		}
	}
}

// Poll runs one enumerate-diff-update cycle.
func (t *Tracker) Poll() error {
	sessions, err := t.enum.Enumerate()
	if err != nil {
		return err
	}
	t.apply(Index(sessions), time.Now())
	return nil
}

// apply updates metrics from a fresh session set. Separated from Poll so tests
// can drive it with an explicit clock.
func (t *Tracker) apply(current map[SessionKey]Session, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	started, ended, _ := Diff(t.sessions, current)

	// Accrue time for everyone who was already signed in before applying this
	// poll's changes, so a session that just ended still gets its final slice.
	t.accrueLocked(now)

	for _, s := range ended {
		canonical, ok := t.resolve(s.RawUser)
		if !ok {
			continue
		}
		state := t.users[canonical]
		if state == nil {
			continue
		}
		state.sessions--
		if state.sessions <= 0 {
			delete(t.users, canonical)
			t.metrics.UserSessionActive.WithLabelValues(canonical, t.hostname).Set(0)
			t.metrics.UserSessionDuration.WithLabelValues(canonical, t.hostname).Set(0)
			t.logger.Info("user session ended", "user", canonical, "session", s.ID)
		}
	}

	for _, s := range started {
		canonical, ok := t.resolve(s.RawUser)
		if !ok {
			continue
		}
		loginTime := s.LoginTime
		if loginTime.IsZero() || loginTime.After(now) {
			loginTime = now
		}

		state := t.users[canonical]
		if state == nil {
			state = &userState{earliestLogin: loginTime, lastAccrual: now}
			t.users[canonical] = state
		} else if loginTime.Before(state.earliestLogin) {
			state.earliestLogin = loginTime
		}
		state.sessions++

		t.metrics.UserSessionActive.WithLabelValues(canonical, t.hostname).Set(1)

		// Sessions already open at startup are adopted, not counted: they are not
		// new logons, and counting them would resurrect the restart-inflation bug.
		if t.seeded {
			t.metrics.UserSessionLogins.WithLabelValues(canonical, t.hostname).Inc()
			t.logger.Info("user session started", "user", canonical, "session", s.ID)
		} else {
			t.logger.Info("adopted existing user session", "user", canonical, "session", s.ID,
				"since", loginTime.Format(time.RFC3339))
		}
	}

	// Refresh the live duration gauge for everyone still signed in.
	for canonical, state := range t.users {
		t.metrics.UserSessionDuration.WithLabelValues(canonical, t.hostname).
			Set(now.Sub(state.earliestLogin).Seconds())
	}

	t.sessions = current
	t.seeded = true
}

// accrueLocked adds elapsed wall-clock time to each signed-in user's session
// counter. Time is counted once per user even when they hold several OS
// sessions — two windows open on one machine is still one person occupying it.
func (t *Tracker) accrueLocked(now time.Time) {
	for canonical, state := range t.users {
		if state.lastAccrual.IsZero() || !now.After(state.lastAccrual) {
			state.lastAccrual = now
			continue
		}
		elapsed := now.Sub(state.lastAccrual).Seconds()
		state.lastAccrual = now
		t.metrics.UserSessionSecondsTotal.WithLabelValues(canonical, t.hostname).Add(elapsed)
	}
}

// ActiveUsers returns the canonical usernames currently signed in. Used by the
// status command and tests.
func (t *Tracker) ActiveUsers() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.users))
	for canonical := range t.users {
		out = append(out, canonical)
	}
	return out
}
