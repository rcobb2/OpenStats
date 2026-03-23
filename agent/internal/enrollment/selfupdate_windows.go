//go:build windows

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

	c.logger.Info("downloading update", "url", url)

	tempFile := filepath.Join(os.TempDir(), "openlabstats-update.msi")
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

	cmd := exec.Command("msiexec.exe", "/i", tempFile, "/qn", "REBOOT=ReallySuppress")
	if err := cmd.Start(); err != nil {
		c.logger.Error("failed to launch msiexec", "error", err)
		return
	}

	c.logger.Info("msiexec launched, agent will likely restart now")
}
