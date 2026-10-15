//go:build darwin

package enrollment

import (
	neturl "net/url"
	"os"
	"os/exec"
	"strings"
)

func (c *Client) executeSelfUpdate(url, checksum string) {
	if !strings.HasPrefix(url, "http") {
		url = c.serverURL + url
	}

	// Reject URLs that point to a different host than the configured server.
	// This prevents a misconfigured or compromised server from redirecting the
	// agent to download an arbitrary executable.
	if parsed, err := neturl.Parse(url); err != nil || !c.isTrustedUpdateHost(parsed.Hostname()) {
		c.logger.Error("untrusted update URL rejected", "url", url)
		return
	}

	c.logger.Info("downloading macOS update", "url", url)

	// A randomized name (not a fixed "openlabstats-update.pkg") so a second
	// download triggered before this install finishes can't overwrite the
	// file this instance is about to execute — defense in depth alongside
	// the cmd.Wait() below, which is what actually keeps a second download
	// from being triggered in the first place.
	out, err := os.CreateTemp(os.TempDir(), "openlabstats-update-*.pkg")
	if err != nil {
		c.logger.Error("failed to create temp file for update", "error", err)
		return
	}
	tempFile := out.Name()
	defer os.Remove(tempFile)
	defer out.Close() // covers error-path early returns

	// downloadUpdate uses its own long-timeout client (the shared one's 10s
	// covers the whole body read, so packages on slow links never finished),
	// checks the status and fails instead of truncating an oversized package.
	if err := c.downloadUpdate(url, out); err != nil {
		c.logger.Error("failed to download update", "error", err)
		return
	}
	// Must close before passing to installer; defer will close again (harmless).
	out.Close()

	if err := verifyDownloadChecksum(tempFile, checksum); err != nil {
		c.logger.Error("downloaded update failed integrity check, aborting install", "error", err)
		return
	}

	c.logger.Info("update downloaded, launching installer", "path", tempFile)

	// Run as root (agent is a LaunchDaemon, already root).
	cmd := exec.Command("installer", "-pkg", tempFile, "-target", "/")
	if err := cmd.Start(); err != nil {
		c.logger.Error("failed to launch installer", "error", err)
		return
	}

	// Block until the install actually finishes (or this process is killed by
	// its postinstall script restarting the daemon — in that case we never
	// get here, which is fine). updateInProgress must stay held for the
	// install's real duration: releasing it right after Start() let a
	// heartbeat mid-install see the same stale AgentVersion, fetch the same
	// update URL again, and launch a second concurrent installer against this
	// agent's own temp download.
	if err := cmd.Wait(); err != nil {
		c.logger.Error("installer exited with error", "error", err)
		return
	}

	c.logger.Info("installer completed, launchd will restart agent after update")
}
