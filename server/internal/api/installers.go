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
	LabName       string `json:"labName"`
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

	installCmd := "msiexec /i openlabstats-agent.msi /qn " +
		"SERVERADDRESS=" + req.ServerAddress + " " +
		"PORT=" + intToStr(req.Port)
	if latestMSI != "" {
		installCmd = fmt.Sprintf(`msiexec /i "%s" /qn SERVERADDRESS=%s PORT=%d`, latestMSI, req.ServerAddress, req.Port)
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

// GetLatestInstallerURL returns the relative download path of the newest MSI,
// by scanning the public/installers directory (no DB required).
func (s *Server) GetLatestInstallerURL() string {
	filename, err := findLatestMSI(s.cfg.Server.PublicDir)
	if err != nil || filename == "" {
		return ""
	}
	return "/installers/" + filename
}

// DownloadLatestInstaller serves the latest MSI file directly.
// @Router /api/v1/installers/latest [get]
func (s *Server) DownloadLatestInstaller(w http.ResponseWriter, r *http.Request) {
	filename, err := findLatestMSI(s.cfg.Server.PublicDir)
	if err != nil || filename == "" {
		writeError(w, http.StatusNotFound, "no installer available")
		return
	}
	msiPath := filepath.Join(s.cfg.Server.PublicDir, "installers", filename)
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, msiPath)
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

// findLatestMSI looks in <publicDir>/installers/ for the newest .msi file.
func findLatestMSI(publicDir string) (string, error) {
	installersDir := filepath.Join(publicDir, "installers")
	var files []fs.DirEntry
	entries, err := os.ReadDir(installersDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".msi") {
			files = append(files, e)
		}
	}
	if len(files) == 0 {
		return "", nil
	}
	// Sort by name descending – works well with our semver-based naming.
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() > files[j].Name()
	})
	return files[0].Name(), nil
}

func intToStr(i int) string {
	return fmt.Sprintf("%d", i)
}
