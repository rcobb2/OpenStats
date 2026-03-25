package api

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	installCmd := fmt.Sprintf(`msiexec /i "%s" /qn SERVERADDRESS="%s" PORT=%d`, msiName, req.ServerAddress, req.Port)
	if req.Building != "" {
		installCmd += fmt.Sprintf(` BUILDING="%s"`, req.Building)
	}
	if req.Room != "" {
		installCmd += fmt.Sprintf(` ROOM="%s"`, req.Room)
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
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, path)
}

// forceAgentUpdate marks an agent for update on its next heartbeat by returning
// the latest installer URL regardless of version.
func forceAgentUpdateURL(publicDir string) string {
	filename, err := findLatestMSI(publicDir)
	if err != nil || filename == "" {
		return ""
	}
	return "/api/v1/installers/latest"
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
	// Sort by name descending — works well with semver-based naming.
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() > files[j].Name()
	})
	return files[0].Name(), nil
}

func intToStr(i int) string {
	return fmt.Sprintf("%d", i)
}
