//go:build windows

package enrollment

import (
	"fmt"
	"io"
	neturl "net/url"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

	tempFile := filepath.Join(os.TempDir(), "openlabstats-update.msi")
	out, err := os.Create(tempFile)
	if err != nil {
		c.logger.Error("failed to create temp file for update", "error", err)
		return
	}
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

	// Pass the current config values as MSI properties so the installer's
	// PowerShell custom action re-applies them after overwriting agent.yaml.
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

	c.logger.Info("msiexec launched, agent will likely restart now")
}
