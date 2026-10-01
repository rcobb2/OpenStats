//go:build windows

package enrollment

import (
	"fmt"
	"io"
	neturl "net/url"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

func (c *Client) executeSelfUpdate(url string) {
	if !strings.HasPrefix(url, "http") {
		url = c.serverURL + url
	}

	// Reject URLs that point to a different host than the configured server.
	if parsed, err := neturl.Parse(url); err != nil || !c.isTrustedUpdateHost(parsed.Hostname()) {
		c.logger.Error("untrusted update URL rejected", "url", url)
		return
	}

	c.logger.Info("downloading update", "url", url)

	// A randomized name (not a fixed "openlabstats-update.msi") so a second
	// download triggered before this install finishes — see updateInProgress's
	// doc comment on why that should no longer happen now that we wait below,
	// but defense in depth is cheap here — can't overwrite the file this
	// instance is about to execute.
	out, err := os.CreateTemp(os.TempDir(), "openlabstats-update-*.msi")
	if err != nil {
		c.logger.Error("failed to create temp file for update", "error", err)
		return
	}
	tempFile := out.Name()
	defer os.Remove(tempFile)
	defer out.Close() // covers error-path early returns

	resp, err := c.client.Get(url)
	if err != nil {
		c.logger.Error("failed to download update", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("failed to download update: unexpected status", "status", resp.StatusCode)
		return
	}

	_, err = io.Copy(out, io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		c.logger.Error("failed to save update to disk", "error", err)
		return
	}
	// Must close before passing to msiexec; defer will close again (harmless).
	out.Close()

	c.logger.Info("update downloaded, launching installer", "path", tempFile)

	// Pass the current config values as MSI properties; the installer writes
	// them to the registry via native WiX RegistryValue elements, which
	// override_windows.go layers over agent.yaml on the next start.
	args := []string{"/i", tempFile, "/qn", "REBOOT=ReallySuppress",
		"SERVERADDRESS=" + c.serverURL,
		fmt.Sprintf("PORT=%d", c.port),
	}
	if c.building != "" {
		args = append(args, "BUILDING="+c.building)
	}
	if c.room != "" {
		args = append(args, "ROOM="+c.room)
	}
	cmd := exec.Command("msiexec.exe", args...)
	if err := cmd.Start(); err != nil {
		c.logger.Error("failed to launch msiexec", "error", err)
		return
	}

	// Block until the install actually finishes (or this process is killed
	// by it, e.g. the MSI's ServiceControl stopping this very service — in
	// that case we never get here, which is fine). updateInProgress must stay
	// held for the install's real duration: releasing it right after Start()
	// let a heartbeat mid-install see the same stale AgentVersion, fetch the
	// same update URL again, and launch a second concurrent msiexec against
	// this agent's own temp download.
	if err := cmd.Wait(); err != nil {
		c.logger.Error("msiexec exited with error", "error", err)
		return
	}

	c.logger.Info("msiexec completed, agent will likely restart now")
}
