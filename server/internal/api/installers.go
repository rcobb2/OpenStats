package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GenerateInstallerRequest is the payload for generating a custom MSI.
type GenerateInstallerRequest struct {
	ServerAddress string `json:"serverAddress"`
	Port          int    `json:"port"`
	Building      string `json:"building"`
	Room          string `json:"room"`
}

// GenerateInstaller godoc
// @Summary      Generate a custom MSI installer
// @Description  Returns a download URL for the latest pre-built MSI with an msiexec command.
// @Tags         installers
// @Accept       json
// @Produce      json
// @Param        body  body  GenerateInstallerRequest  true  "Installer configuration"
// @Success      200   {object}  map[string]string
// @Failure      400   {object}  map[string]string
// @Router       /api/v1/installers/generate [post]
func (s *Server) GenerateInstaller(w http.ResponseWriter, r *http.Request) {
	var req GenerateInstallerRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ServerAddress == "" {
		writeError(w, http.StatusBadRequest, "serverAddress is required")
		return
	}

	// Normalize before baking the address into the msiexec command. A bare
	// hostname makes every agent request fail with `unsupported protocol
	// scheme ""`, so the agent installs cleanly and then never registers.
	req.ServerAddress = normalizeServerAddress(req.ServerAddress)

	if req.Port == 0 {
		req.Port = 9183
	}

	// Find the latest MSI from the public installers directory.
	latestMSI, _ := findLatestMSI(s.cfg.Server.PublicDir)
	downloadURL := ""
	if latestMSI != "" {
		downloadURL = "/installers/" + latestMSI
	}

	msiName := "openlabstats-agent.msi"
	if latestMSI != "" {
		msiName = latestMSI
	}
	sanitize := func(s string) string { return strings.ReplaceAll(s, `"`, ``) }
	installCmd := fmt.Sprintf(`msiexec /i "%s" /qn SERVERADDRESS="%s" PORT=%d`, sanitize(msiName), sanitize(req.ServerAddress), req.Port)
	if req.Building != "" {
		installCmd += fmt.Sprintf(` BUILDING="%s"`, sanitize(req.Building))
	}
	if req.Room != "" {
		installCmd += fmt.Sprintf(` ROOM="%s"`, sanitize(req.Room))
	}

	response := map[string]string{
		"status":         "ready",
		"installCommand": installCmd,
		"downloadUrl":    downloadURL,
		"serverAddress":  req.ServerAddress,
		"port":           intToStr(req.Port),
	}

	s.logger.Info("installer generation requested",
		"serverAddress", req.ServerAddress,
		"port", req.Port,
	)

	writeJSON(w, http.StatusOK, response)
}

// GetLatestInstallerURL returns the relative download path of the newest installer
// appropriate for the given OS version string. macOS agents receive a .pkg;
// everything else receives a .msi.
func (s *Server) GetLatestInstallerURL(osVersion ...string) string {
	os := ""
	if len(osVersion) > 0 {
		os = osVersion[0]
	}
	filename, err := findLatestInstaller(s.cfg.Server.PublicDir, os)
	if err != nil || filename == "" {
		return ""
	}
	return "/installers/" + filename
}

// GetLatestInstallerVersion returns the dotted version string of the newest
// installer appropriate for the given OS (e.g. "0.4.0"), or "" if none exists.
// It resolves the exact same file GetLatestInstallerURL would serve, so the
// rollout target always matches what the agent would actually download.
func (s *Server) GetLatestInstallerVersion(osVersion string) string {
	filename, err := findLatestInstaller(s.cfg.Server.PublicDir, osVersion)
	if err != nil || filename == "" {
		return ""
	}
	return installerVersionRe.FindString(filename)
}

// installerChecksumEntry caches a file's SHA-256 against the mtime/size it was
// computed from, so re-hashing isn't repeated on every heartbeat from every
// agent in the fleet for a file that hasn't changed.
type installerChecksumEntry struct {
	modTime time.Time
	size    int64
	sha256  string
}

// installerChecksumForURL returns the lowercase hex SHA-256 of the file an
// updateUrl (as returned by GetLatestInstallerURL/decideRolloutUpdate/
// ForceAgentUpdate) points to, or "" if it can't be computed (file missing,
// read error). The agent treats "" as "no checksum available" and installs
// without verification rather than refusing the update outright — this is a
// corruption/tamper check layered on top of the existing trusted-host check,
// not a replacement for it, so a transient hashing failure shouldn't stall
// the whole fleet's updates.
func (s *Server) installerChecksumForURL(updateURL string) string {
	if updateURL == "" {
		return ""
	}
	path := filepath.Join(s.cfg.Server.PublicDir, "installers", filepath.Base(updateURL))

	info, err := os.Stat(path)
	if err != nil {
		return ""
	}

	s.checksumCacheMu.Lock()
	if cached, ok := s.checksumCache[path]; ok && cached.modTime.Equal(info.ModTime()) && cached.size == info.Size() {
		s.checksumCacheMu.Unlock()
		return cached.sha256
	}
	s.checksumCacheMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		s.logger.Warn("failed to open installer for checksum", "path", path, "error", err)
		return ""
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		s.logger.Warn("failed to hash installer", "path", path, "error", err)
		return ""
	}
	sum := hex.EncodeToString(h.Sum(nil))

	s.checksumCacheMu.Lock()
	s.checksumCache[path] = installerChecksumEntry{modTime: info.ModTime(), size: info.Size(), sha256: sum}
	s.checksumCacheMu.Unlock()
	return sum
}

// DownloadLatestInstaller serves the latest installer file directly.
// Use ?platform=mac to download the macOS .pkg; omit for the Windows .msi.
// @Router /api/v1/installers/latest [get]
func (s *Server) DownloadLatestInstaller(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	osHint := ""
	if platform == "mac" || platform == "macos" || platform == "darwin" {
		osHint = "macOS"
	}
	filename, err := findLatestInstaller(s.cfg.Server.PublicDir, osHint)
	if err != nil || filename == "" {
		writeError(w, http.StatusNotFound, "no installer available")
		return
	}
	path := filepath.Join(s.cfg.Server.PublicDir, "installers", filename)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, path)
}

// findLatestInstaller looks in <publicDir>/installers/ for the newest installer
// file matching the agent's platform. macOS agents get a .pkg; Windows agents
// get a .msi. Detection is tiered:
//  1. Explicit strings: osVersion contains "macos", "darwin", or "windows" → definitive.
//  2. Numeric-only versions (e.g. "14.3.1", "26.3"): macOS format — agents that haven't
//     yet applied the "macOS " prefix fix fall here. Prefer .pkg, fall back to .msi.
//  3. Anything else (e.g. "Windows Server 2022"): prefer .msi, fall back to .pkg.
func findLatestInstaller(publicDir, osVersion string) (string, error) {
	lower := strings.ToLower(osVersion)

	var primary, fallback string
	switch {
	case strings.Contains(lower, "macos") || strings.Contains(lower, "darwin"):
		primary, fallback = ".pkg", ".msi"
	case strings.Contains(lower, "windows"):
		primary, fallback = ".msi", ".pkg"
	case isNumericVersion(osVersion):
		// Bare numeric version — almost certainly macOS (Windows versions contain
		// non-numeric text). Prefer .pkg but fall back to .msi.
		primary, fallback = ".pkg", ".msi"
	default:
		primary, fallback = ".msi", ".pkg"
	}

	if f, err := findLatestByExt(publicDir, primary); err == nil && f != "" {
		return f, nil
	}
	return findLatestByExt(publicDir, fallback)
}

// isNumericVersion returns true if s consists only of digits and dots (e.g. "14.3.1", "26.3").
func isNumericVersion(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c != '.' && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// findLatestMSI is kept for internal callers that always want a Windows installer.
func findLatestMSI(publicDir string) (string, error) {
	return findLatestByExt(publicDir, ".msi")
}

func findLatestByExt(publicDir, ext string) (string, error) {
	installersDir := filepath.Join(publicDir, "installers")
	entries, err := os.ReadDir(installersDir)
	if err != nil {
		return "", err
	}
	var files []fs.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ext) {
			files = append(files, e)
		}
	}
	if len(files) == 0 {
		return "", nil
	}
	// Sort newest-version-first. A plain string sort is wrong here: it orders
	// "...-0.1.9.msi" above "...-0.1.10.msi" because '9' > '1', so the server
	// would hand agents an older installer than the one it has on disk and
	// outdated agents could never move forward.
	sort.Slice(files, func(i, j int) bool {
		ni, nj := files[i].Name(), files[j].Name()
		if c := compareVersions(extractVersion(ni), extractVersion(nj)); c != 0 {
			return c > 0
		}
		// Same version (e.g. -arm64 vs -universal) or no version at all:
		// fall back to name order so the pick stays deterministic.
		return ni > nj
	})
	return files[0].Name(), nil
}

// normalizeServerAddress mirrors enrollment.NormalizeServerURL on the agent:
// assume https when no scheme is given and drop trailing slashes, so the
// generated install command always produces a working reportURL.
func normalizeServerAddress(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	return strings.TrimRight(s, "/")
}

// installerVersionRe matches the first dotted-numeric run in a filename, e.g.
// "openlabstats-agent-0.1.10-universal.pkg" -> "0.1.10".
var installerVersionRe = regexp.MustCompile(`\d+(?:\.\d+)+`)

// extractVersion pulls the version components out of an installer filename.
// Returns nil when the name carries no recognizable version, which sorts below
// every versioned file.
func extractVersion(name string) []int {
	m := installerVersionRe.FindString(name)
	if m == "" {
		return nil
	}
	parts := strings.Split(m, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// compareVersions returns >0 when a is newer than b, <0 when older, 0 when
// equal. Missing trailing components count as zero, so 0.1 and 0.1.0 are equal.
func compareVersions(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var av, bv int
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return 0
}

func intToStr(i int) string {
	return fmt.Sprintf("%d", i)
}
