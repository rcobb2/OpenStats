package enrollment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

// AgentVersion is the single source of truth for the agent version string.
// Also update the installer WiX version in agent/installer/openlabstats.wxs when bumping.
const AgentVersion = "0.1.6"

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
	Settings  *SystemSettings `json:"settings"`
	UpdateURL string          `json:"updateUrl,omitempty"`
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
}

// NewClient creates a new enrollment client.
func NewClient(serverURL string, agentPort int, building, room string, logger *slog.Logger) *Client {
	return &Client{
		serverURL: serverURL,
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
	settings, updateURL, err := c.doRegister(ctx)
	if err != nil {
		c.logger.Warn("registration failed", "error", err)
	}
	return settings, updateURL
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

func (c *Client) doRegister(ctx context.Context) (*SystemSettings, string, error) {
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
		return nil, "", fmt.Errorf("marshal registration: %w", err)
	}

	url := c.serverURL + "/api/v1/agents/register"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("send registration: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("registration failed: status %d", resp.StatusCode)
	}

	var res RegisterAgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, "", fmt.Errorf("decode registration response: %w", err)
	}

	return res.Settings, res.UpdateURL, nil
}

// RunHeartbeat periodically registers with the server.
func (c *Client) RunHeartbeat(ctx context.Context, defaultInterval time.Duration) {
	currentInterval := defaultInterval

	s, updateURL := c.Register(ctx)
	if s != nil && s.HeartbeatIntervalSeconds > 0 {
		currentInterval = time.Duration(s.HeartbeatIntervalSeconds) * time.Second
	}

	// Also check for update on startup.
	// Server-directed updates take priority - always update when server sends URL.
	if updateURL != "" {
		c.logger.Info("startup: server-directed update received, initiating self-update", "url", updateURL)
		go c.executeSelfUpdate(updateURL)
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
					// Server-directed update: always update when server sends URL
					c.logger.Info("server-directed update received, initiating self-update", "url", updateURL)
					go c.executeSelfUpdate(updateURL)
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
