package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"

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
	store     *store.Store
	cfg       *config.Config
	discovery *discovery.FileSD
	logger    *slog.Logger
}

// NewRouter creates the chi router with all API routes.
func NewRouter(st *store.Store, cfg *config.Config, disc *discovery.FileSD, logger *slog.Logger) http.Handler {
	s := &Server{
		store:     st,
		cfg:       cfg,
		discovery: disc,
		logger:    logger,
	}

	r := chi.NewRouter()

	// Middleware.
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
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

	// Grafana Proxy - allow frontend to access dashboards without direct exposure.
	// We point this to the internal container address 'grafana:3000'.
	grafanaURL, _ := url.Parse("http://openlabstats-grafana:3000")
	proxy := httputil.NewSingleHostReverseProxy(grafanaURL)
	r.Mount("/grafana", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip the /grafana prefix so it matches Grafana's internal routing.
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/grafana")
		proxy.ServeHTTP(w, r)
	}))

	// API v1 routes.
	r.Route("/api/v1", func(r chi.Router) {
		// Agents
		r.Route("/agents", func(r chi.Router) {
			r.Post("/register", s.RegisterAgent)
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

	// Serve installer MSI files directly (used by agents for self-update).
	installersDir := filepath.Join(s.cfg.Server.PublicDir, "installers")
	r.Get("/installers/*", func(w http.ResponseWriter, req *http.Request) {
		filename := strings.TrimPrefix(req.URL.Path, "/installers/")
		http.ServeFile(w, req, filepath.Join(installersDir, filename))
	})

	// SPA frontend (catch-all).
	r.Get("/*", spaHandler(s.cfg.Server.PublicDir))

	return r
}

func spaHandler(publicDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(publicDir, r.URL.Path)
		_, err := os.Stat(p)
		if os.IsNotExist(err) || r.URL.Path == "/" {
			http.ServeFile(w, r, filepath.Join(publicDir, "index.html"))
			return
		}
		http.FileServer(http.Dir(publicDir)).ServeHTTP(w, r)
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
	return json.NewDecoder(r.Body).Decode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
