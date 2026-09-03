package enrollment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rcobb/openlabstats-agent/internal/urlutil"
)

// AgentVersion is the single source of truth for the agent version string.
// The installer picks it up automatically: agent/installer/build.ps1 greps this
// constant and passes -d Version=... to WiX, which Package.wxs consumes as
// $(var.Version). No second edit is needed. (The file this comment used to name,
// openlabstats.wxs, does not exist — the manifest is Package.wxs.)
const AgentVersion = "0.4.3"

// RegisterRequest matches the server's RegisterAgentRequest.
type RegisterRequest struct {
	ID           string `json:"id"`
	Hostname     string `json:"hostname"`
	IPAddress    string `json:"ipAddress"`
	OSVersion    string `json:"osVersion"`
	AgentVersion string `json:"agentVersion"`
	Port         int    `json:"port"`
	Building     string `json:"building"`
	Room         string `json:"room"`
}

type RegisterAgentResponse struct {
	Settings        *SystemSettings `json:"settings"`
	UpdateURL       string          `json:"updateUrl,omitempty"`
	IgnoredExeNames []string        `json:"ignoredExeNames,omitempty"`
	UserPolicy      *UserPolicy     `json:"userPolicy,omitempty"`
}

// UserPolicy is the server-managed user-tracking policy. Ignored accounts never
// produce metrics, and Aliases merges usernames that belong to one person —
// typically a macOS shortname into its domain account.
type UserPolicy struct {
	StripDomain    bool              `json:"stripDomain"`
	IgnorePatterns []string          `json:"ignorePatterns"`
	Aliases        map[string]string `json:"aliases"`
}

type SystemSettings struct {
	HeartbeatIntervalSeconds int    `json:"heartbeatIntervalSeconds"`
	UpdateIntervalSeconds    int    `json:"updateIntervalSeconds"`
	StaleTimeoutDays         int    `json:"staleTimeoutDays"`
	MinAgentVersion          string `json:"minAgentVersion"`
	MaintenanceWindowStart   string `json:"maintenanceWindowStart"`
	MaintenanceWindowEnd     string `json:"maintenanceWindowEnd"`
}

// Client handles agent registration with the central server.
type Client struct {
	serverURL string
	port      int
	building  string
	room      string
	osVersion string
	logger    *slog.Logger
	client    *http.Client

	ignoredMu       sync.RWMutex
	ignoredExeNames []string

	// updateInProgress guards executeSelfUpdate. Heartbeats fire on every
	// interval and the server may return the same update URL for several ticks;
	// without this a slow download/install would stack concurrent installers
	// (each re-downloading the full package) until the service finally restarted.
	updateInProgress atomic.Bool

	// onUserPolicy, when set, receives the policy from every successful
	// registration so the agent can apply changes without restarting.
	onUserPolicy func(*UserPolicy)
}

// launchSelfUpdate starts a self-update at most once at a time, after a small
// random jitter. The jitter smooths the herd when the server offers an update to
// several agents in the same window; the in-progress guard prevents subsequent
// heartbeat ticks from re-triggering while an install is already running. If the
// update fails (e.g. a download error) the guard is released so a later window
// can retry; on a successful install the service is torn down and restarted, so
// the reset is moot.
func (c *Client) launchSelfUpdate(url string) {
	if !c.updateInProgress.CompareAndSwap(false, true) {
		c.logger.Debug("self-update already in progress, skipping duplicate trigger")
		return
	}
	go func() {
		defer c.updateInProgress.Store(false)
		jitter := time.Duration(rand.Intn(selfUpdateJitterSeconds)) * time.Second
		c.logger.Info("self-update scheduled", "jitter", jitter)
		time.Sleep(jitter)
		c.executeSelfUpdate(url)
	}()
}

// selfUpdateJitterSeconds bounds the random delay before a self-update download.
const selfUpdateJitterSeconds = 30

// WithUserPolicyHandler registers a callback invoked with the server's user
// policy on each successful registration.
func (c *Client) WithUserPolicyHandler(fn func(*UserPolicy)) *Client {
	c.onUserPolicy = fn
	return c
}

// NormalizeServerURL is retained here as the enrollment-facing entry point;
// the implementation lives in internal/urlutil so internal/config can apply the
// same rules to installer-supplied registry values.
func NormalizeServerURL(raw string) string {
	return urlutil.NormalizeServerURL(raw)
}

// NewClient creates a new enrollment client.
func NewClient(serverURL string, agentPort int, building, room string, logger *slog.Logger) *Client {
	return &Client{
		serverURL: NormalizeServerURL(serverURL),
		port:      agentPort,
		building:  building,
		room:      room,
		logger:    logger,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// WithOSVersion sets the OS version string included in registration payloads.
func (c *Client) WithOSVersion(v string) *Client {
	c.osVersion = v
	return c
}

// Register sends a registration/heartbeat to the central server.
func (c *Client) Register(ctx context.Context) (*SystemSettings, string) {
	res, err := c.doRegister(ctx)
	if err != nil {
		// Include the target so a misconfigured server address is diagnosable
		// from the agent log instead of looking like a transient network blip.
		c.logger.Error("registration failed", "error", err, "serverURL", c.serverURL)
		return nil, ""
	}
	if res.IgnoredExeNames != nil {
		c.ignoredMu.Lock()
		c.ignoredExeNames = res.IgnoredExeNames
		c.ignoredMu.Unlock()
	}
	if res.UserPolicy != nil && c.onUserPolicy != nil {
		c.onUserPolicy(res.UserPolicy)
	}
	return res.Settings, res.UpdateURL
}

// FetchUserPolicy retrieves the user policy without registering. Used at startup
// so the first process events are already filtered and canonicalized, ahead of
// the first heartbeat.
func (c *Client) FetchUserPolicy(ctx context.Context) (*UserPolicy, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverURL+"/api/v1/users/policy", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var policy UserPolicy
	if err := json.NewDecoder(resp.Body).Decode(&policy); err != nil {
		return nil, err
	}
	return &policy, nil
}

// GetIgnoredExeNames returns the server-provided list of exe names to suppress.
// Updated on every heartbeat.
func (c *Client) GetIgnoredExeNames() []string {
	c.ignoredMu.RLock()
	defer c.ignoredMu.RUnlock()
	out := make([]string, len(c.ignoredExeNames))
	copy(out, c.ignoredExeNames)
	return out
}

// GetSettings fetches settings from the server without registering.
func (c *Client) GetSettings(ctx context.Context) (*SystemSettings, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.serverURL+"/api/v1/settings", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}

	var settings SystemSettings
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		return nil, err
	}

	return &settings, nil
}

func (c *Client) doRegister(ctx context.Context) (*RegisterAgentResponse, error) {
	hostname, _ := os.Hostname()
	ip := getOutboundIP()

	req := RegisterRequest{
		ID:           hostname,
		Hostname:     hostname,
		IPAddress:    ip,
		OSVersion:    c.osVersion,
		AgentVersion: AgentVersion,
		Port:         c.port,
		Building:     c.building,
		Room:         c.room,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal registration: %w", err)
	}

	url := c.serverURL + "/api/v1/agents/register"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send registration: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registration failed: status %d", resp.StatusCode)
	}

	var res RegisterAgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode registration response: %w", err)
	}

	return &res, nil
}

// RunHeartbeat periodically registers with the server.
func (c *Client) RunHeartbeat(ctx context.Context, defaultInterval time.Duration) {
	currentInterval := defaultInterval

	s, updateURL := c.Register(ctx)
	if s != nil && s.HeartbeatIntervalSeconds > 0 {
		currentInterval = time.Duration(s.HeartbeatIntervalSeconds) * time.Second
	}

	// Also check for update on startup.
	if updateURL != "" {
		if s != nil && IsInMaintenanceWindow(s.MaintenanceWindowStart, s.MaintenanceWindowEnd) {
			c.logger.Info("startup: server-directed update received, initiating self-update", "url", updateURL)
			c.launchSelfUpdate(updateURL)
		} else {
			c.logger.Info("startup: update available but outside maintenance window, deferring", "url", updateURL)
		}
	}

	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s, updateURL = c.Register(ctx)
			c.logger.Debug("heartbeat completed", "updateURL", updateURL)
			if s != nil {
				if s.HeartbeatIntervalSeconds > 0 {
					newInterval := time.Duration(s.HeartbeatIntervalSeconds) * time.Second
					if newInterval != currentInterval {
						c.logger.Info("adjusting heartbeat interval", "old", currentInterval, "new", newInterval)
						currentInterval = newInterval
						ticker.Reset(currentInterval)
					}
				}

				if updateURL != "" {
					if IsInMaintenanceWindow(s.MaintenanceWindowStart, s.MaintenanceWindowEnd) {
						c.logger.Info("server-directed update received, initiating self-update", "url", updateURL)
						c.launchSelfUpdate(updateURL)
					} else {
						c.logger.Info("update available but outside maintenance window, deferring", "url", updateURL)
					}
				}
			}
		}
	}
}

func IsInMaintenanceWindow(startStr, endStr string) bool {
	if startStr == "" || endStr == "" {
		return true
	}

	parseHHMM := func(s string) (int, bool) {
		var h, m int
		if n, _ := fmt.Sscanf(s, "%d:%d", &h, &m); n != 2 {
			return 0, false
		}
		if h < 0 || h > 23 || m < 0 || m > 59 {
			return 0, false
		}
		return h*60 + m, true
	}

	startMinutes, ok1 := parseHHMM(startStr)
	endMinutes, ok2 := parseHHMM(endStr)
	if !ok1 || !ok2 {
		return true // treat invalid config as always in window (safe default)
	}

	if startMinutes == endMinutes {
		return false // zero-length window means never in maintenance
	}

	now := time.Now()
	currentMinutes := now.Hour()*60 + now.Minute()

	if startMinutes < endMinutes {
		return currentMinutes >= startMinutes && currentMinutes <= endMinutes
	}
	// Wraps midnight: e.g. 23:00–05:00
	return currentMinutes >= startMinutes || currentMinutes <= endMinutes
}

// isTrustedUpdateHost returns true when host matches the host portion of
// c.serverURL. This prevents a server from redirecting self-updates to an
// arbitrary third-party host.
func (c *Client) isTrustedUpdateHost(host string) bool {
	parsed, err := neturl.Parse(c.serverURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(host, parsed.Hostname())
}

// PushMetrics fetches the agent's local /metrics endpoint and pushes the body
// to the server. This lets the server aggregate metrics from all agents and
// expose them to Prometheus, bypassing any network firewall blocking port 9183.
func (c *Client) PushMetrics(ctx context.Context) error {
	localURL := fmt.Sprintf("http://localhost:%d/metrics", c.port)
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, localURL, nil)
	if err != nil {
		return err
	}
	getResp, err := c.client.Do(getReq)
	if err != nil {
		return fmt.Errorf("fetch local metrics: %w", err)
	}
	defer getResp.Body.Close()
	body, err := io.ReadAll(getResp.Body)
	if err != nil {
		return fmt.Errorf("read local metrics: %w", err)
	}

	hostname, _ := os.Hostname()
	pushURL := c.serverURL + "/api/v1/agents/metrics"
	pushReq, err := http.NewRequestWithContext(ctx, http.MethodPost, pushURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	pushReq.Header.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	pushReq.Header.Set("X-Agent-ID", hostname)

	pushResp, err := c.client.Do(pushReq)
	if err != nil {
		return fmt.Errorf("push metrics: %w", err)
	}
	defer pushResp.Body.Close()
	if pushResp.StatusCode != http.StatusNoContent && pushResp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %d", pushResp.StatusCode)
	}
	return nil
}

// RunMetricsPush pushes the agent's metrics snapshot to the server at the
// given interval. Runs until ctx is cancelled.
func (c *Client) RunMetricsPush(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.PushMetrics(ctx); err != nil {
				c.logger.Warn("metrics push failed", "error", err)
			} else {
				c.logger.Debug("metrics pushed to server")
			}
		}
	}
}

// getOutboundIP returns the best IP address for Prometheus to scrape this agent.
// It prefers the Tailscale CGNAT address (100.64.0.0/10) so that Prometheus,
// which runs under the ts-openstats sidecar, can reach agents on the Tailnet
// regardless of LAN topology. Falls back to the default outbound LAN IP.
func getOutboundIP() string {
	if ip := getTailscaleIP(); ip != "" {
		return ip
	}
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)
	return addr.IP.String()
}

// getTailscaleIP returns the machine's Tailscale IP by scanning network
// interfaces for an address in the Tailscale CGNAT range (100.64.0.0/10).
func getTailscaleIP() string {
	_, tsNet, _ := net.ParseCIDR("100.64.0.0/10")
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.To4() != nil && tsNet.Contains(ip) {
				return ip.String()
			}
		}
	}
	return ""
}
