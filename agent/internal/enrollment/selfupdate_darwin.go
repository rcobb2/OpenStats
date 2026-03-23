//go:build darwin

package enrollment

import (
	"io"
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

	c.logger.Info("downloading macOS update", "url", url)

	tempFile := filepath.Join(os.TempDir(), "openlabstats-update.pkg")
	out, err := os.Create(tempFile)
	if err != nil {
		c.logger.Error("failed to create temp file for update", "error", err)
		return
	}
	defer out.Close()

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

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		c.logger.Error("failed to save update to disk", "error", err)
		return
	}
	out.Close()

	c.logger.Info("update downloaded, launching installer", "path", tempFile)

	// Run as root (agent is a LaunchDaemon, already root).
	cmd := exec.Command("installer", "-pkg", tempFile, "-target", "/")
	if err := cmd.Start(); err != nil {
		c.logger.Error("failed to launch installer", "error", err)
		return
	}

	c.logger.Info("installer launched, launchd will restart agent after update")
}
