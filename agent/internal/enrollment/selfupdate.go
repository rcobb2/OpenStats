package enrollment

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// maxUpdateSize caps the update package we are willing to download.
	maxUpdateSize = 200 << 20
	// updateDownloadTimeout bounds the whole download. The shared client's 10s
	// timeout is fine for heartbeats but far too short for a package on a slow link.
	updateDownloadTimeout = 15 * time.Minute
)

// downloadUpdate fetches url into dst using a long-timeout client. It fails on
// a non-200 status or a body larger than maxUpdateSize, so a truncated package
// is never handed to the installer.
func (c *Client) downloadUpdate(url string, dst io.Writer) error {
	client := &http.Client{Timeout: updateDownloadTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	n, err := io.Copy(dst, io.LimitReader(resp.Body, maxUpdateSize+1))
	if err != nil {
		return fmt.Errorf("saving update: %w", err)
	}
	if n > maxUpdateSize {
		return fmt.Errorf("update package exceeds %d byte limit", int64(maxUpdateSize))
	}
	return nil
}
