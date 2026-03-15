package api

import (
	"fmt"
	"net/http"
)

// GenerateInstallerRequest is the payload for generating a custom MSI.
type GenerateInstallerRequest struct {
	ServerAddress string `json:"serverAddress"`
	Port          int    `json:"port"`
	LabName       string `json:"labName"`
}

// GenerateInstaller godoc
// @Summary      Generate a custom MSI installer
// @Description  Generates an MSI installer pre-configured with the given server address and port. Returns a download URL. NOTE: Full MSI generation requires WiX Toolset on the server; this endpoint currently returns the install command with the appropriate properties.
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

	// For now, return the install command. Full MSI generation can be
	// implemented when WiX is available on the server (or pre-built MSIs
	// are stored and MST transforms are applied).
	response := map[string]string{
		"status": "ready",
		"installCommand": "msiexec /i openlabstats-agent.msi /qn " +
			"SERVERADDRESS=" + req.ServerAddress + " " +
			"PORT=" + intToStr(req.Port),
		"serverAddress": req.ServerAddress,
		"port":          intToStr(req.Port),
	}

	s.logger.Info("installer generation requested",
		"serverAddress", req.ServerAddress,
		"port", req.Port,
	)

	writeJSON(w, http.StatusOK, response)
}

// GetSettings godoc
// @Summary      Get server settings
// @Description  Returns the current server configuration (non-sensitive fields).
// @Tags         settings
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Router       /api/v1/settings [get]
func (s *Server) GetSettings(w http.ResponseWriter, r *http.Request) {
	settings := map[string]interface{}{
		"server": map[string]interface{}{
			"port": s.cfg.Server.Port,
		},
		"prometheus": map[string]interface{}{
			"url": s.cfg.Prom.URL,
		},
		"fileSD": map[string]interface{}{
			"outputPath": s.cfg.FileSD.OutputPath,
		},
	}
	writeJSON(w, http.StatusOK, settings)
}

func intToStr(i int) string {
	return fmt.Sprintf("%d", i)
}
