package monitor

import (
	"log/slog"
	"sync"
	"time"
)

// ProcessSession represents a tracked process group from start to stop.
// Consumers see a single session per application launch, regardless of
// how many child processes were spawned.
type ProcessSession struct {
	PID             uint32
	ExeName         string
	ExePath         string
	User            string
	StartTime       time.Time
	StopTime        time.Time
	Active          bool
	CheckpointDelta time.Duration // time since last checkpoint (for incremental counter updates)
	ForegroundDelta time.Duration // foreground active time since last checkpoint
}

// processGroup tracks a root process and all its children as a single unit.
type processGroup struct {
	RootPID        uint32
	ExeName        string // from the root process
	ExePath        string // from the root process
	User           string
	FamilyKey      string
	StartTime       time.Time
	LastCheckpoint  time.Time
	ForegroundDelta time.Duration
	MemberPIDs      map[uint32]bool
}

// Tracker manages the state of all active process groups.
type Tracker struct {
	mu           sync.RWMutex
	groups       map[uint32]*processGroup // rootPID -> group
	pidToGroup   map[uint32]uint32        // any member PID -> rootPID
	familyGroups map[string]uint32        // familyKey -> rootPID (for normalizer fallback)
	logger       *slog.Logger
}

// NewTracker creates a new process tracker.
func NewTracker(logger *slog.Logger) *Tracker {
	return &Tracker{
		groups:       make(map[uint32]*processGroup),
		pidToGroup:   make(map[uint32]uint32),
		familyGroups: make(map[string]uint32),
		logger:       logger,
	}
}

// OnProcessStart records a new process starting.
// parentPID is the parent process ID from the WMI event (0 if unknown).
// familyKey is the normalizer family key (empty if none).
// Returns true if this created a new process group (i.e., a genuinely new app launch).
func (t *Tracker) OnProcessStart(pid, parentPID uint32, exeName, exePath, user, familyKey string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Strategy 1: Check if parent PID belongs to an existing group that has a
	// family key. We require a family key so that shells and launchers (which
	// have no family) don't absorb every process they spawn.
	if parentPID != 0 {
		if rootPID, ok := t.pidToGroup[parentPID]; ok {
			group := t.groups[rootPID]
			if group.FamilyKey != "" {
				group.MemberPIDs[pid] = true
				t.pidToGroup[pid] = rootPID
				t.logger.Debug("process joined group via parent",
					"pid", pid, "exe", exeName, "parentPID", parentPID, "rootPID", rootPID)
				return false
			}
		}
	}

	// Strategy 2: Check if family key matches an existing active group.
	if familyKey != "" {
		if rootPID, ok := t.familyGroups[familyKey]; ok {
			if group, exists := t.groups[rootPID]; exists {
				group.MemberPIDs[pid] = true
				t.pidToGroup[pid] = rootPID
				t.logger.Debug("process joined group via family",
					"pid", pid, "exe", exeName, "family", familyKey, "rootPID", rootPID)
				return false
			}
			// Stale entry — clean it up and fall through to create new group.
			delete(t.familyGroups, familyKey)
		}
	}

	// No existing group found — create a new one.
	now := time.Now()
	group := &processGroup{
		RootPID:        pid,
		ExeName:        exeName,
		ExePath:        exePath,
		User:           user,
		FamilyKey:      familyKey,
		StartTime:      now,
		LastCheckpoint: now,
		MemberPIDs:     map[uint32]bool{pid: true},
	}
	t.groups[pid] = group
	t.pidToGroup[pid] = pid
	if familyKey != "" {
		t.familyGroups[familyKey] = pid
	}

	t.logger.Debug("new process group created",
		"pid", pid, "exe", exeName, "user", user, "family", familyKey)
	return true
}

// OnProcessStop marks a process as stopped.
// Returns the completed session only when the last member of a group exits.
func (t *Tracker) OnProcessStop(pid uint32) *ProcessSession {
	t.mu.Lock()
	defer t.mu.Unlock()

	rootPID, ok := t.pidToGroup[pid]
	if !ok {
		return nil
	}

	group := t.groups[rootPID]
	if group == nil {
		// Shouldn't happen, but clean up.
		delete(t.pidToGroup, pid)
		return nil
	}

	delete(group.MemberPIDs, pid)
	delete(t.pidToGroup, pid)

	// If group still has members, just log and continue.
	if len(group.MemberPIDs) > 0 {
		t.logger.Debug("process left group, group still active",
			"pid", pid, "rootPID", rootPID, "remaining", len(group.MemberPIDs))
		return nil
	}

	// Group is now empty — finalize it.
	now := time.Now()
	checkpointDelta := now.Sub(group.LastCheckpoint)
	foregroundDelta := group.ForegroundDelta
	delete(t.groups, rootPID)
	if group.FamilyKey != "" {
		delete(t.familyGroups, group.FamilyKey)
	}

	t.logger.Debug("process group ended",
		"rootPID", rootPID,
		"exe", group.ExeName,
		"duration", now.Sub(group.StartTime),
		"family", group.FamilyKey,
	)

	return &ProcessSession{
		PID:             group.RootPID,
		ExeName:         group.ExeName,
		ExePath:         group.ExePath,
		User:            group.User,
		StartTime:       group.StartTime,
		StopTime:        now,
		Active:          false,
		CheckpointDelta: checkpointDelta,
		ForegroundDelta: foregroundDelta,
	}
}

// ActiveSessions returns a snapshot of all currently active process groups as sessions.
func (t *Tracker) ActiveSessions() []*ProcessSession {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]*ProcessSession, 0, len(t.groups))
	for _, g := range t.groups {
		result = append(result, &ProcessSession{
			PID:       g.RootPID,
			ExeName:   g.ExeName,
			ExePath:   g.ExePath,
			User:      g.User,
			StartTime: g.StartTime,
			Active:    true,
		})
	}
	return result
}

// ActiveCount returns the number of currently tracked process groups.
func (t *Tracker) ActiveCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.groups)
}

// GetProcessUser returns the user associated with a tracked process's group, or empty string.
func (t *Tracker) GetProcessUser(pid uint32) string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	rootPID, ok := t.pidToGroup[pid]
	if !ok {
		return ""
	}
	if g, ok := t.groups[rootPID]; ok {
		return g.User
	}
	return ""
}

// CheckpointActive captures accumulated usage time for all active groups
// since their last checkpoint, resets the checkpoint timers, and returns
// snapshots. Callers should add CheckpointDelta to usage counters but
// should NOT increment launch counters or persist to the DB.
func (t *Tracker) CheckpointActive() []*ProcessSession {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	var sessions []*ProcessSession
	for _, group := range t.groups {
		delta := now.Sub(group.LastCheckpoint)
		group.LastCheckpoint = now
		fgDelta := group.ForegroundDelta
		group.ForegroundDelta = 0

		sessions = append(sessions, &ProcessSession{
			PID:             group.RootPID,
			ExeName:         group.ExeName,
			ExePath:         group.ExePath,
			User:            group.User,
			StartTime:       group.StartTime,
			StopTime:        now,
			Active:          true,
			CheckpointDelta: delta,
			ForegroundDelta: fgDelta,
		})
	}
	return sessions
}

// IncrementForeground attributes active foreground time to a tracked process group.
func (t *Tracker) IncrementForeground(pid uint32, duration time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	rootPID, ok := t.pidToGroup[pid]
	if !ok {
		return
	}

	if group, ok := t.groups[rootPID]; ok {
		group.ForegroundDelta += duration
	}
}

// Reconcile updates the tracker based on a snapshot of actually running PIDs.
// Removes groups whose members are all gone.
func (t *Tracker) Reconcile(runningPIDs map[uint32]bool) []*ProcessSession {
	t.mu.Lock()
	defer t.mu.Unlock()

	// First pass: remove stale member PIDs from all groups.
	for pid := range t.pidToGroup {
		if !runningPIDs[pid] {
			delete(t.pidToGroup, pid)
			for _, group := range t.groups {
				delete(group.MemberPIDs, pid)
			}
		}
	}

	// Second pass: finalize any groups that are now empty.
	now := time.Now()
	var stopped []*ProcessSession
	for rootPID, group := range t.groups {
		if len(group.MemberPIDs) == 0 {
			delete(t.groups, rootPID)
			if group.FamilyKey != "" {
				delete(t.familyGroups, group.FamilyKey)
			}
			stopped = append(stopped, &ProcessSession{
				PID:       group.RootPID,
				ExeName:   group.ExeName,
				ExePath:   group.ExePath,
				User:      group.User,
				StartTime: group.StartTime,
				StopTime:  now,
				Active:    false,
			})
			t.logger.Debug("reconciled stale process group",
				"rootPID", rootPID, "exe", group.ExeName)
		}
	}
	return stopped
}
