package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	"github.com/rcobb/openlabstats-server/internal/config"
	"github.com/rcobb/openlabstats-server/internal/discovery"
	"github.com/rcobb/openlabstats-server/internal/store"
)

// Server holds shared dependencies for all API handlers.
type Server struct {
	store        *store.Store
	cfg          *config.Config
	discovery    *discovery.FileSD
	logger       *slog.Logger
	metricsStore *MetricsStore
	promClient   *http.Client
}

// NewRouter creates the chi router with all API routes.
func NewRouter(st *store.Store, cfg *config.Config, disc *discovery.FileSD, logger *slog.Logger) http.Handler {
	s := &Server{
		store:        st,
		cfg:          cfg,
		discovery:    disc,
		logger:       logger,
		metricsStore: newMetricsStore(),
		promClient:   &http.Client{Timeout: 15 * time.Second},
	}

	r := chi.NewRouter()

	// Middleware.
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// Health check.
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Get("/api/docs/*", httpSwagger.Handler(
		httpSwagger.URL("/api/docs/doc.json"),
	))

	// API v1 routes.
	r.Route("/api/v1", func(r chi.Router) {
		// Agents
		r.Route("/agents", func(r chi.Router) {
			r.Post("/register", s.RegisterAgent)
			r.Post("/metrics", s.PushAgentMetrics)
			r.Get("/", s.ListAgents)
			r.Get("/{agentID}", s.GetAgent)
			r.Put("/{agentID}/lab", s.AssignAgentToLab)
			r.Delete("/{agentID}", s.DeleteAgent)
			r.Post("/{agentID}/force-update", s.ForceAgentUpdate)
		})

		// Labs
		r.Route("/labs", func(r chi.Router) {
			r.Get("/", s.ListLabs)
			r.Post("/", s.CreateLab)
			r.Get("/{labID}", s.GetLab)
			r.Put("/{labID}", s.UpdateLab)
			r.Delete("/{labID}", s.DeleteLab)
		})

		// Software mappings
		r.Route("/mappings", func(r chi.Router) {
			r.Get("/", s.ListMappings)
			r.Get("/agent", s.GetAgentMappings) // Agent-facing endpoint (software-map.json format)
			r.Post("/", s.CreateMapping)
			r.Put("/", s.UpdateMapping)
			r.Delete("/{mappingID}", s.DeleteMapping)
			r.Patch("/{mappingID}/ignore", s.ToggleMappingIgnored)
		})

		// Reports
		r.Route("/reports", func(r chi.Router) {
			r.Get("/top-apps", s.ReportTopAppsUsage)
			r.Get("/top-apps-by-launches", s.ReportTopAppsByLaunches)
			r.Get("/top-apps-by-foreground", s.ReportTopAppsByForegroundTime)
			r.Get("/bottom-apps-by-launches", s.ReportBottomAppsByLaunches)
			r.Get("/bottom-apps-by-foreground", s.ReportBottomAppsByForegroundTime)
			r.Get("/usage-by-lab", s.ReportUsageByLab)
			r.Get("/active-users", s.ReportActiveUsers)
			r.Get("/top-devices-by-sessions", s.ReportTopDevicesBySessionCount)
			r.Get("/top-users-by-logins", s.ReportTopUsersByLoginCount)
			r.Get("/top-users-by-session-time", s.ReportTopUsersBySessionTime)
			r.Get("/avg-session-time", s.ReportAvgSessionTime)
			r.Get("/summary", s.ReportSummary)
		})

		// Installer generation & download
		r.Route("/installers", func(r chi.Router) {
			r.Post("/generate", s.GenerateInstaller)
			r.Get("/latest", s.DownloadLatestInstaller)
		})

		// Settings
		r.Route("/settings", func(r chi.Router) {
			r.Get("/", s.GetSettings)
			r.Put("/", s.UpdateSettings)
		})
	})

	// Aggregated agent metrics endpoint for Prometheus scraping.
	// Agents push snapshots via POST /api/v1/agents/metrics; Prometheus pulls here.
	r.Get("/metrics/agents", s.ServeAgentMetrics)

	// Serve installer MSI files directly (used by agents for self-update).
	installersDir := filepath.Join(s.cfg.Server.PublicDir, "installers")
	r.Get("/installers/*", func(w http.ResponseWriter, req *http.Request) {
		filename := filepath.Base(strings.TrimPrefix(req.URL.Path, "/installers/"))
		if filename == "" || filename == "." {
			http.NotFound(w, req)
			return
		}
		http.ServeFile(w, req, filepath.Join(installersDir, filename))
	})

	// SPA frontend (catch-all).
	r.Get("/*", spaHandler(s.cfg.Server.PublicDir))

	return r
}

func spaHandler(publicDir string) http.HandlerFunc {
	fs := http.FileServer(http.Dir(publicDir))
	return func(w http.ResponseWriter, r *http.Request) {
		// filepath.Clean prevents path traversal before the os.Stat probe.
		cleaned := filepath.Clean("/" + r.URL.Path)
		p := filepath.Join(publicDir, cleaned)
		_, err := os.Stat(p)
		if os.IsNotExist(err) || r.URL.Path == "/" {
			http.ServeFile(w, r, filepath.Join(publicDir, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	}
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
