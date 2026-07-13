package api

import (
	"net/http"
	"runtime"

	"github.com/rcobb/openlabstats-server/internal/version"
)

type buildInfoResponse struct {
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
}

// ReportBuildInfo godoc
// @Summary      Server build information
// @Description  Returns the running server's version, git commit, build date, and Go runtime version.
// @Tags         meta
// @Produce      json
// @Success      200  {object}  buildInfoResponse
// @Router       /api/v1/version [get]
func (s *Server) ReportBuildInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, buildInfoResponse{
		Version:   version.Version,
		GitCommit: version.GitCommit,
		BuildDate: version.BuildDate,
		GoVersion: runtime.Version(),
	})
}
