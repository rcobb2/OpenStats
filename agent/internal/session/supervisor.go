package session

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"
)

// ReconcileInterval is how often the supervisor compares running helpers to
// the sessions that should have one.
const ReconcileInterval = 30 * time.Second

// errNoPrivilege means the agent cannot launch into other users' sessions
// (console/test mode rather than SYSTEM/root). Retrying cannot help.
var errNoPrivilege = errors.New("insufficient privilege to launch session helpers")

// target is a session that should have a helper. Key identifies it across
// reconciles; ID is the platform's session identity (Windows session id, macOS
// uid) and is what the sample validator checks against.
type target struct {
	Key string
	ID  uint32
}

// proc is a running helper. Kill must be safe to call repeatedly and after
// exit; Wait blocks until the process is gone and releases its resources.
type proc struct {
	Stdout io.ReadCloser
	Kill   func()
	Wait   func()
}

// platform is the OS-specific half of the supervisor.
type platform interface {
	desired() ([]target, error)
	spawn(t target) (*proc, error)
	// validator returns the anti-spoofing check for samples from t's helper.
	// The helper runs as the user, so the PID it reports is untrusted.
	validator(t target) func(pid uint32) bool
	close()
}

type helperState struct {
	running    bool
	proc       *proc
	lastStart  time.Time
	lastExit   time.Time
	quickFails int
	spawnWarn  bool // a spawn failure for this session has already been logged
}

// Supervisor keeps one session helper alive per desired session and feeds
// their samples into a Hub.
type Supervisor struct {
	hub    *Hub
	logger *slog.Logger
	plat   platform
	now    func() time.Time

	mu       sync.Mutex
	states   map[string]*helperState
	disabled bool
	wg       sync.WaitGroup
}

// NewSupervisor builds the platform supervisor. Samples go to hub.
func NewSupervisor(hub *Hub, logger *slog.Logger) *Supervisor {
	s := &Supervisor{hub: hub, logger: logger, now: time.Now, states: make(map[string]*helperState)}
	plat, err := newPlatform(logger)
	if err != nil {
		logger.Warn("session helpers unavailable, falling back to in-process foreground detection", "error", err)
		s.disabled = true
		return s
	}
	s.plat = plat
	return s
}

// Run reconciles until ctx is cancelled, then kills every helper.
func (s *Supervisor) Run(ctx context.Context) {
	defer s.shutdown()
	if s.disabled {
		return
	}
	s.reconcile()
	t := time.NewTicker(ReconcileInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.disabled {
				return
			}
			s.reconcile()
		}
	}
}

func (s *Supervisor) reconcile() {
	want, err := s.plat.desired()
	if err != nil {
		s.logger.Warn("session helper: could not enumerate sessions", "error", err)
		return
	}
	wanted := make(map[string]target, len(want))
	for _, t := range want {
		wanted[t.Key] = t
	}

	s.mu.Lock()
	// Stop helpers whose session is gone (logoff, console user change) and
	// forget idle bookkeeping for sessions that no longer exist.
	for key, st := range s.states {
		if _, ok := wanted[key]; ok {
			continue
		}
		if st.running {
			st.proc.Kill()
		} else {
			delete(s.states, key)
		}
	}
	s.mu.Unlock()

	now := s.now()
	for key, t := range wanted {
		s.mu.Lock()
		st := s.states[key]
		if st == nil {
			st = &helperState{}
			s.states[key] = st
		}
		skip := st.running || (!st.lastStart.IsZero() && now.Before(nextSpawnAllowed(st.lastStart, st.lastExit, st.quickFails)))
		s.mu.Unlock()
		if skip {
			continue
		}
		if !s.start(t, st, now) {
			return // privilege problem: stop trying this round (and ever)
		}
	}
}

// start spawns a helper for t. It returns false only when spawning can never
// work in this process, after disabling the supervisor.
func (s *Supervisor) start(t target, st *helperState, now time.Time) bool {
	p, err := s.plat.spawn(t)
	if err != nil {
		if errors.Is(err, errNoPrivilege) {
			s.logger.Warn("not running with privilege to start session helpers (console/test mode?); "+
				"using in-process foreground detection only", "error", err)
			s.disabled = true
			return false
		}
		s.mu.Lock()
		first := !st.spawnWarn
		st.spawnWarn = true
		st.lastStart, st.lastExit = now, now
		var tripped bool
		st.quickFails, tripped = nextQuickFails(st.quickFails, now, now)
		s.mu.Unlock()
		// Only the first failure per session is a Warn; the 30s retry would
		// otherwise repeat the same line forever.
		if first || tripped {
			s.logger.Warn("session helper: spawn failed", "session", t.Key, "error", err)
		} else {
			s.logger.Debug("session helper: spawn failed", "session", t.Key, "error", err)
		}
		return true
	}

	s.mu.Lock()
	st.running, st.proc, st.lastStart, st.spawnWarn = true, p, now, false
	s.wg.Add(1)
	s.mu.Unlock()
	s.logger.Info("session helper started", "session", t.Key)
	go s.watch(t, st, p, now)
	return true
}

// watch is the per-helper goroutine: it reads samples until the helper's
// pipe closes, then reaps it and records the exit for the back-off policy.
func (s *Supervisor) watch(t target, st *helperState, p *proc, started time.Time) {
	defer s.wg.Done()
	err := ReadSamples(p.Stdout, s.plat.validator(t), func(sm Sample) { s.hub.Update(t.Key, sm) })
	// Kill first: a helper that overran the line limit is still alive.
	p.Kill()
	p.Wait()
	p.Stdout.Close()
	s.hub.Remove(t.Key)

	exited := s.now()
	s.mu.Lock()
	st.running, st.proc, st.lastExit = false, nil, exited
	var tripped bool
	st.quickFails, tripped = nextQuickFails(st.quickFails, started, exited)
	s.mu.Unlock()

	s.logger.Info("session helper exited", "session", t.Key, "lifetime", exited.Sub(started).Round(time.Second), "readError", err)
	if tripped {
		s.logger.Warn("session helper keeps exiting immediately; backing off", "session", t.Key, "retryIn", longBackoff)
	}
}

func (s *Supervisor) shutdown() {
	s.mu.Lock()
	for _, st := range s.states {
		if st.running {
			st.proc.Kill()
		}
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.logger.Warn("session helpers did not exit promptly on shutdown")
	}
	if s.plat != nil {
		s.plat.close()
	}
}
