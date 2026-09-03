//go:build darwin

package monitor

import (
	"os"
	"testing"
)

// TestDiagnosticRootProcessVisibility is a throwaway CI diagnostic. PID 1
// (launchd) failed even under sudo, which rules out a privilege-level
// explanation — but the existing, pre-elevation-feature production code
// already depends on getProcBSDInfo succeeding for arbitrary pids (the whole
// app-usage tracking feature calls it on every running process every poll,
// and that already works in the field), so a blanket failure seems unlikely.
// This sweeps every real running pid to find out whether PID 1 specifically
// is special-cased, or something broader is wrong. Run explicitly via -run
// in CI; not part of the regular suite's signal.
func TestDiagnosticRootProcessVisibility(t *testing.T) {
	t.Logf("running as uid=%d euid=%d", os.Getuid(), os.Geteuid())
	self := uint32(os.Getpid())
	if info, ok := getProcBSDInfo(self); ok {
		t.Logf("getProcBSDInfo(self=%d) = %+v, ok=true (expected: own pid always readable)", self, ok)
		_ = info
	} else {
		t.Errorf("getProcBSDInfo(self=%d) failed — even reading our own process's info doesn't work, something is fundamentally broken in the cgo call", self)
	}

	pids := listAllPIDs()
	t.Logf("listAllPIDs() returned %d pids", len(pids))

	var successes, failures, root0Successes int
	var sampleFailures, sampleRoot0 []string
	for _, pid := range pids {
		if pid == 0 {
			continue
		}
		info, ok := getProcBSDInfo(pid)
		if !ok {
			failures++
			if len(sampleFailures) < 10 {
				sampleFailures = append(sampleFailures, itoa(pid))
			}
			continue
		}
		successes++
		if info.uid == 0 {
			root0Successes++
			if len(sampleRoot0) < 10 {
				sampleRoot0 = append(sampleRoot0, info.exeName+"("+itoa(pid)+")")
			}
		}
	}
	t.Logf("across %d real pids: %d succeeded (%d of those uid=0), %d failed", len(pids), successes, root0Successes, failures)
	t.Logf("sample successful root(uid=0) reads: %v", sampleRoot0)
	t.Logf("sample failed pids: %v", sampleFailures)
}

func itoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
