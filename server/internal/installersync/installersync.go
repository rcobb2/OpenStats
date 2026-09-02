// Package installersync keeps the local installers directory in sync with the
// latest GitHub release, so newly published agent builds (.pkg/.msi) become
// available for the staggered auto-update rollout without a manual fetch step.
//
// It only ever downloads assets that aren't already present, writing to a temp
// file and renaming into place (atomic, and — because the write happens from
// inside the server container — the file inherits the volume's SELinux label,
// avoiding the mislabeling that host-side copies hit). It never deletes existing
// installers, so older versions remain available for agents mid-upgrade.
package installersync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxAssetBytes = 300 << 20 // hard cap per downloaded installer
	httpTimeout   = 5 * time.Minute
)

// githubAPIBase is the GitHub REST API base; overridable in tests.
var githubAPIBase = "https://api.github.com"

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type release struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

// Run polls the latest GitHub release for `repo` every `interval`, downloading
// any new .pkg/.msi assets into <publicDir>/installers/. It returns when ctx is
// cancelled. A first sync runs shortly after startup.
func Run(ctx context.Context, publicDir, repo string, interval time.Duration, logger *slog.Logger) {
	client := &http.Client{Timeout: httpTimeout}
	// Small initial delay so startup isn't blocked on a network call.
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := syncOnce(ctx, client, publicDir, repo, logger); err != nil {
				logger.Warn("installer sync failed", "error", err)
			}
			timer.Reset(interval)
		}
	}
}

func syncOnce(ctx context.Context, client *http.Client, publicDir, repo string, logger *slog.Logger) error {
	rel, err := latestRelease(ctx, client, repo)
	if err != nil {
		return err
	}
	installersDir := filepath.Join(publicDir, "installers")
	if err := os.MkdirAll(installersDir, 0o755); err != nil {
		return fmt.Errorf("ensure installers dir: %w", err)
	}

	fetched := 0
	for _, a := range rel.Assets {
		lower := strings.ToLower(a.Name)
		if !strings.HasSuffix(lower, ".pkg") && !strings.HasSuffix(lower, ".msi") {
			continue
		}
		dest := filepath.Join(installersDir, filepath.Base(a.Name))
		if _, err := os.Stat(dest); err == nil {
			continue // already have it
		}
		if err := download(ctx, client, a.URL, dest); err != nil {
			logger.Warn("failed to download installer asset", "asset", a.Name, "error", err)
			continue
		}
		logger.Info("installer sync: downloaded new asset", "release", rel.TagName, "asset", a.Name)
		fetched++
	}
	if fetched > 0 {
		logger.Info("installer sync complete", "release", rel.TagName, "new", fetched)
	}
	return nil
}

func latestRelease(ctx context.Context, client *http.Client, repo string) (*release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", githubAPIBase, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases API returned %d", resp.StatusCode)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	return &rel, nil
}

// download streams url to a temp file in the same directory as dest, then renames
// it into place so a partial download is never observed as a valid installer.
func download(ctx context.Context, client *http.Client, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".installer-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, maxAssetBytes)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}
