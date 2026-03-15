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
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const agentVersion = "0.1.0"

// RegisterRequest matches the server's RegisterAgentRequest.
	Port         int    `json:"port"`
	Building     string `json:"building"`
	Room         string `json:"room"`
	UpdateURL    string `json:"updateUrl,omitempty"`
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
}

type RegisterResponse struct {
	Settings *SystemSettings `json:"settings"`
}

// Client handles agent registration with the central server.
type Client struct {
	serverURL string
	port      int
	building  string
	room      string
	logger    *slog.Logger
	client    *http.Client
}

// NewClient creates a new enrollment client.
// serverURL is the base URL of the central server (e.g., "https://server.campus.edu:8080").
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

	var respData RegisterAgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		c.logger.Warn("failed to decode registration response", "error", err)
		return nil, "" // Still registered successfully
	}

	c.logger.Info("registered with server", "url", url, "hostname", hostname)
	return respData.Settings, respData.UpdateURL
}

// Register sends a registration/heartbeat to the central server.
// Returns settings if provided by server.
func (c *Client) Register(ctx context.Context) (*SystemSettings, string) {
	settings, updateURL, err := c.doRegister(ctx)
	if err != nil {
		c.logger.Warn("registration failed", "error", err)
	}
	return settings, updateURL
}

func (c *Client) doRegister(ctx context.Context) (*SystemSettings, string, error) {
	hostname, _ := os.Hostname()
	ip := getOutboundIP()

	req := RegisterRequest{
		ID:           hostname,
		Hostname:     hostname,
		IPAddress:    ip,
		OSVersion:    "", // TODO: read from registry
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
		return nil, "", nil // Consider it a success without settings
	}

	return res.Settings, res.UpdateURL
}

// RunHeartbeat periodically registers with the server.
// Blocks until ctx is cancelled.
func (c *Client) RunHeartbeat(ctx context.Context, defaultInterval time.Duration) {
	currentInterval := defaultInterval
	
	// Register immediately on startup.
	s, updateURL := c.Register(ctx)
	if s != nil && s.HeartbeatIntervalSeconds > 0 {
		currentInterval = time.Duration(s.HeartbeatIntervalSeconds) * time.Second
	}

	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s, updateURL = c.Register(ctx)
			if s != nil {
				if s.HeartbeatIntervalSeconds > 0 {
					newInterval := time.Duration(s.HeartbeatIntervalSeconds) * time.Second
					if newInterval != currentInterval {
						c.logger.Info("adjusting heartbeat interval", "old", currentInterval, "new", newInterval)
						currentInterval = newInterval
						ticker.Reset(currentInterval)
					}
				}

				// Check for updates if URL provided.
				if updateURL != "" {
					if isInMaintenanceWindow(s.MaintenanceWindowStart, s.MaintenanceWindowEnd) {
						c.logger.Info("within maintenance window, initiating self-update", "url", updateURL)
						go c.executeSelfUpdate(updateURL)
					} else {
						c.logger.Debug("update available but outside maintenance window", "start", s.MaintenanceWindowStart, "end", s.MaintenanceWindowEnd)
					}
				}
			}
		}
	}
}

func isInMaintenanceWindow(startStr, endStr string) bool {
	if startStr == "" || endStr == "" {
		return true // No window set, always permit
	}

	now := time.Now()
	// Parse current time as HH:mm and convert to minutes since midnight for easy comparison
	currentMinutes := now.Hour()*60 + now.Minute()

	var startH, startM, endH, endM int
	fmt.Sscanf(startStr, "%d:%d", &startH, &startM)
	fmt.Sscanf(endStr, "%d:%d", &endH, &endM)

	startMinutes := startH*60 + startM
	endMinutes := endH*60 + endM

	if startMinutes < endMinutes {
		// Standard window (e.g., 22:00 to 04:00 doesn't happen here)
		return currentMinutes >= startMinutes && currentMinutes <= endMinutes
	} else {
		// Overnight window (e.g., 22:00 to 04:00)
		return currentMinutes >= startMinutes || currentMinutes <= endMinutes
	}
}

func (c *Client) executeSelfUpdate(url string) {
	// 1. Resolve relative path to absolute
	if !strings.HasPrefix(url, "http") {
		url = c.serverURL + url
	}

	c.logger.Info("downloading update", "url", url)
	
	// 2. Download to temp file
	tempFile := filepath.Join(os.TempDir(), "openlabstats-update.msi")
	out, err := os.Create(tempFile)
	if err != nil {
		c.logger.Error("failed to create temp file for update", "error", err)
		return
	}
	defer out.Close()

	resp, err := c.client.Get(url)
	if err != nil {
		c.logger.Error("failed to download update", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("failed to download update: unexpected status", "status", resp.StatusCode)
		return
	}

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		c.logger.Error("failed to save update to disk", "error", err)
		return
	}
	out.Close()

	c.logger.Info("update downloaded, launching installer", "path", tempFile)

	// 3. Launch msiexec silently
	// We use /qn for silent, /i for install, and restart the service.
	// We also use REBOOT=ReallySuppress to avoid unexpected lab reboots.
	cmd := exec.Command("msiexec.exe", "/i", tempFile, "/qn", "REBOOT=ReallySuppress")
	if err := cmd.Start(); err != nil {
		c.logger.Error("failed to launch msiexec", "error", err)
		return
	}

	c.logger.Info("msiexec launched, agent will likely restart now")
	// The agent will be killed by the MSI installer as it replaces the binary.
}

// getOutboundIP returns the preferred outbound IP of this machine.
func getOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)
	return addr.IP.String()
}
