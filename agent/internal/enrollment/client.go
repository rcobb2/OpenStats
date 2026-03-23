package enrollment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

const agentVersion = "0.1.3"

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
		AgentVersion: agentVersion,
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
		return nil, "", nil
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

	now := time.Now()
	currentMinutes := now.Hour()*60 + now.Minute()

	var startH, startM, endH, endM int
	fmt.Sscanf(startStr, "%d:%d", &startH, &startM)
	fmt.Sscanf(endStr, "%d:%d", &endH, &endM)

	startMinutes := startH*60 + startM
	endMinutes := endH*60 + endM

	if startMinutes < endMinutes {
		return currentMinutes >= startMinutes && currentMinutes <= endMinutes
	} else {
		return currentMinutes >= startMinutes || currentMinutes <= endMinutes
	}
}

func getOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)
	return addr.IP.String()
}
