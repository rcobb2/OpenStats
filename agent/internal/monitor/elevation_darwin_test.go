//go:build darwin

package monitor

import (
	"os"
	"os/user"
	"testing"
)

// TestRootEscalationInvokerUsesParentOwner exercises rootEscalationInvoker
// against this test process's own PID as the "parent" — a real, live PID with
// a known owner — verifying attribution comes from the parent, not the
// (hypothetical) elevated child.
func TestRootEscalationInvokerUsesParentOwner(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skipf("could not determine current user: %v", err)
	}
	if me.Uid == "0" {
		t.Skip("test process is running as root; the non-root-parent case can't be exercised")
	}

	invokingUser, escalated := rootEscalationInvoker(0, uint32(os.Getpid()))
	if !escalated {
		t.Fatal("expected escalation to be detected: child uid 0, live non-root parent")
	}
	if invokingUser != me.Username {
		t.Errorf("invokingUser = %q, want %q (this process's own owner)", invokingUser, me.Username)
	}
}

// TestRootEscalationInvokerNonRootChildNeverEscalates confirms an ordinary
// (non-root) child is never reported as an escalation, regardless of parent.
func TestRootEscalationInvokerNonRootChildNeverEscalates(t *testing.T) {
	if _, escalated := rootEscalationInvoker(501, uint32(os.Getpid())); escalated {
		t.Error("a non-root child must never be reported as an escalation")
	}
}

// TestRootEscalationInvokerUnknownParentDoesNotCount verifies that a
// nonexistent (already exited) parent PID is treated as "no one to attribute
// to" and therefore not counted — the opposite bias from the Windows check.
func TestRootEscalationInvokerUnknownParentDoesNotCount(t *testing.T) {
	const bogusPID = 999999999 // extremely unlikely to be a live PID
	if _, escalated := rootEscalationInvoker(0, bogusPID); escalated {
		t.Error("an unreadable parent should never be reported as an escalation")
	}
}
