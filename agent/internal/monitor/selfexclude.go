package monitor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The session helper (`openlabstats-agent session-helper`) runs as the
// signed-in user from the agent's own binary, so to the process trackers it
// looks like an ordinary user application: it would show up as app usage and
// as a launch for every login. It is excluded in code rather than via the
// exclude patterns in agent.yaml because existing installs keep their old
// config file across upgrades.
const agentBaseName = "openlabstats-agent"

var selfPath = resolveSelfPath()

func resolveSelfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe
}

// isAgentProcess reports whether a process image is the agent's own binary.
func isAgentProcess(exeName, exePath string) bool {
	return matchesAgent(exeName, exePath, selfPath, runtime.GOOS == "windows")
}

// matchesAgent compares the image path with the agent's own executable path
// (case-insensitive when foldCase, i.e. on Windows) and, as a fallback for
// processes whose path cannot be read, the image base name.
func matchesAgent(exeName, exePath, self string, foldCase bool) bool {
	eq := func(a, b string) bool {
		if foldCase {
			return strings.EqualFold(a, b)
		}
		return a == b
	}
	if exePath != "" && self != "" {
		p := exePath
		if foldCase {
			p = strings.ReplaceAll(p, "/", `\`)
		}
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		if eq(filepath.Clean(p), filepath.Clean(self)) {
			return true
		}
	}
	return eq(exeName, agentBaseName) || eq(exeName, agentBaseName+".exe")
}
