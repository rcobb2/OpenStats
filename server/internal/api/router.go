package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"golang.org/x/sync/singleflight"

	"github.com/rcobb/openlabstats-server/internal/config"
	"github.com/rcobb/openlabstats-server/internal/discovery"
	"github.com/rcobb/openlabstats-server/internal/store"
)

// promQueryTimeout bounds a single Prometheus request — both promClient's own
// Timeout and the shared singleflight fetch inside cachingTransport, which
// can't inherit any individual caller's deadline (see its RoundTrip).
const promQueryTimeout = 45 * time.Second

// promCacheTTL is how long a successful Prometheus response is served from
// cachingTransport before being treated as stale. Short enough that a report
// page never shows badly outdated numbers, long enough to absorb one page
// load's own panel fan-out and nearby concurrent viewers.
const promCacheTTL = 60 * time.Second

// concurrencyLimitedTransport caps how many requests are in flight to
// Prometheus at once. A single report page fires ~6 panel queries
// simultaneously, and increase() over this fleet's high-cardinality series is
// expensive enough that letting them all hit Prometheus at once causes severe
// contention rather than parallelism: measured in production, 6 concurrent
// 30d queries took 17-72s each (one exceeding even a 45s client timeout),
// versus 8-20s run one at a time. Limiting concurrency trades a short queue
// for keeping each query close to its unconstrained cost instead of degrading
// all of them together.
type concurrencyLimitedTransport struct {
	sem       chan struct{}
	transport http.RoundTripper
}

func (t *concurrencyLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Must select on the request's context, not just block on the semaphore:
	// http.Client.Timeout works by cancelling this context, and a plain `t.sem
	// <- struct{}{}` ignores that entirely — a request stuck waiting for a
	// slot would hang past the client's own timeout instead of failing fast.
	// This matters most for a handler that makes two sequential Prometheus
	// calls (e.g. ReportAvgSessionTime): it needs two turns through the
	// semaphore, so it waits longest under contention.
	select {
	case t.sem <- struct{}{}:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	defer func() { <-t.sem }()
	return t.transport.RoundTrip(req)
}

// cachedPromResponse captures enough of an *http.Response to reconstruct a
// fresh one for each caller — http.Response.Body can only be read once, but
// a cache entry is read by every request that hits it.
type cachedPromResponse struct {
	status int
	header http.Header
	body   []byte
}

func (c cachedPromResponse) toHTTPResponse(req *http.Request) *http.Response {
	return &http.Response{
		Status:        http.StatusText(c.status),
		StatusCode:    c.status,
		Header:        c.header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(c.body)),
		ContentLength: int64(len(c.body)),
		Request:       req,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
	}
}

// cachingTransport adds a short-TTL cache plus request coalescing in front of
// the underlying transport. A report page fires several panel queries at
// once, and — per concurrencyLimitedTransport's doc comment — this fleet's
// query cost is high enough that two people viewing "Last 30 Days" around
// the same time (or one person reloading) would otherwise each pay the full
// cost independently. This collapses identical in-flight requests into one
// upstream call (singleflight) and serves a completed result straight from
// memory for promCacheTTL afterward. Only successful (200) responses are
// cached — a failed query (timeout, upstream error) isn't worth freezing in
// place for other callers.
type cachingTransport struct {
	transport http.RoundTripper
	ttl       time.Duration
	group     singleflight.Group

	mu    sync.Mutex
	cache map[string]cachedEntry
}

type cachedEntry struct {
	resp    cachedPromResponse
	expires time.Time
}

// newCachingTransport constructs a cachingTransport and starts its background
// janitor. RoundTrip only ever adds cache entries, never removes them — every
// distinct query URL is its own key, including one embedding a unique
// &time=<unix-ts> (a custom-range report, parseCustomTimeRange) or
// &start=<ts>&end=<ts> (the utilization charts' query_range calls). Without
// pruning, a long-running server process accumulates one permanent entry
// (full response body included) per distinct custom range ever requested,
// forever — the same unbounded-growth shape as every other leak fixed this
// session, just on the server's own Prometheus-response cache instead of an
// agent-side map.
func newCachingTransport(ttl time.Duration, inner http.RoundTripper) *cachingTransport {
	t := &cachingTransport{
		ttl:       ttl,
		cache:     make(map[string]cachedEntry),
		transport: inner,
	}
	go t.runJanitor()
	return t
}

// prune removes every cache entry that expired before now. Split out from
// runJanitor so it's testable without waiting on a real ticker.
func (t *cachingTransport) prune(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, e := range t.cache {
		if now.After(e.expires) {
			delete(t.cache, k)
		}
	}
}

// runJanitor periodically prunes expired entries. Runs for the lifetime of
// the server process, same as runStaleChecker in cmd/server/main.go — never
// explicitly stopped, since the process itself is the only thing that ends.
func (t *cachingTransport) runJanitor() {
	ticker := time.NewTicker(t.ttl)
	defer ticker.Stop()
	for now := range ticker.C {
		t.prune(now)
	}
}

func (t *cachingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return t.transport.RoundTrip(req)
	}
	key := req.URL.String()

	t.mu.Lock()
	entry, ok := t.cache[key]
	t.mu.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.resp.toHTTPResponse(req), nil
	}

	// The shared fetch runs on its own bounded context, not any one caller's —
	// singleflight.Group.Do lets every concurrent caller for the same key wait
	// on one in-flight call, so the call itself must not be tied to whichever
	// caller happened to arrive first (its context ending shouldn't cut off
	// the others still waiting on the result).
	v, err, _ := t.group.Do(key, func() (interface{}, error) {
		ctx, cancel := context.WithTimeout(context.Background(), promQueryTimeout)
		defer cancel()
		upstreamReq := req.Clone(ctx)

		resp, err := t.transport.RoundTrip(upstreamReq)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		cached := cachedPromResponse{status: resp.StatusCode, header: resp.Header.Clone(), body: body}

		if resp.StatusCode == http.StatusOK {
			t.mu.Lock()
			t.cache[key] = cachedEntry{resp: cached, expires: time.Now().Add(t.ttl)}
			t.mu.Unlock()
		}
		return cached, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(cachedPromResponse).toHTTPResponse(req), nil
}

// Server holds shared dependencies for all API handlers.
type Server struct {
	store        *store.Store
	cfg          *config.Config
	discovery    *discovery.FileSD
	logger       *slog.Logger
	metricsStore *MetricsStore
	promClient   *http.Client

	checksumCacheMu sync.Mutex
	checksumCache   map[string]installerChecksumEntry
}

// NewRouter creates the chi router with all API routes.
func NewRouter(st *store.Store, cfg *config.Config, disc *discovery.FileSD, logger *slog.Logger) http.Handler {
	s := &Server{
		store:        st,
		cfg:          cfg,
		discovery:    disc,
		logger:       logger,
		metricsStore: newMetricsStore(),
		// 15s was too tight: increase() over a growing fleet's high-cardinality
		// (hostname, app) series scales with range, and a 30d report routinely
		// exceeded it (confirmed in production: 14d ~8s, 21d ~15s, 25d+ timed
		// out) — surfaced as every report panel failing on the 30-day view.
		// Transport chain (outermost first): cachingTransport serves/coalesces
		// repeated queries; concurrencyLimitedTransport caps how many distinct
		// queries reach Prometheus at once. See each type's doc comment.
		promClient: &http.Client{
			Timeout: promQueryTimeout,
			Transport: newCachingTransport(promCacheTTL, &concurrencyLimitedTransport{
				sem:       make(chan struct{}, 2),
				transport: http.DefaultTransport,
			}),
		},
		checksumCache: make(map[string]installerChecksumEntry),
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
		// Build info
		r.Get("/version", s.ReportBuildInfo)

		// Agents
		r.Route("/agents", func(r chi.Router) {
			r.Post("/register", s.RegisterAgent)
			r.Post("/metrics", s.PushAgentMetrics)
			r.Get("/", s.ListAgents)
			r.Get("/rollout-status", s.RolloutStatus)
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
			r.Get("/categories", s.ListMappingCategories) // valid categories for manual entry (add/edit forms)
			r.Get("/agent", s.GetAgentMappings)           // Agent-facing endpoint (software-map.json format)
			r.Post("/", s.CreateMapping)
			r.Put("/", s.UpdateMapping)
			r.Delete("/{mappingID}", s.DeleteMapping)
			r.Patch("/{mappingID}/ignore", s.ToggleMappingIgnored)
		})

		// Users: ignore rules and cross-platform identity correlation
		r.Route("/users", func(r chi.Router) {
			r.Get("/", s.ListDiscoveredUsers)
			r.Get("/policy", s.GetUserPolicy) // Also pushed to agents on every heartbeat
			r.Put("/policy", s.UpdateUserPolicy)
			r.Post("/ignore", s.QuickIgnoreUser)
			r.Get("/mappings", s.ListUserMappings)
			r.Put("/mappings", s.UpsertUserMapping)
			r.Post("/mappings", s.UpsertUserMapping)
			r.Delete("/mappings/{mappingID}", s.DeleteUserMapping)
			r.Patch("/mappings/{mappingID}/ignore", s.ToggleUserMappingIgnored)
		})

		// Quick-ignore from reports page (creates or updates mapping to ignored=true)
		r.Post("/reports/ignore-app", s.QuickIgnoreApp)

		// Reports
		r.Route("/reports", func(r chi.Router) {
			r.Get("/top-apps", s.ReportTopAppsUsage)
			r.Get("/top-apps-by-launches", s.ReportTopAppsByLaunches)
			r.Get("/top-apps-by-elevations", s.ReportTopAppsByElevations)
			r.Get("/top-apps-by-foreground", s.ReportTopAppsByForegroundTime)
			r.Get("/bottom-apps-by-launches", s.ReportBottomAppsByLaunches)
			r.Get("/bottom-apps-by-foreground", s.ReportBottomAppsByForegroundTime)
			r.Get("/usage-by-lab", s.ReportUsageByLab)
			r.Get("/utilization-over-time", s.ReportUtilizationOverTime)
			r.Get("/active-users", s.ReportActiveUsers)
			r.Get("/top-devices-by-sessions", s.ReportTopDevicesBySessionCount)
			r.Get("/top-users-by-logins", s.ReportTopUsersByLoginCount)
			r.Get("/top-users-by-elevations", s.ReportTopUsersByElevations)
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
