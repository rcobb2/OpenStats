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

const agentVersion = "0.1.0"

// RegisterRequest matches the server's RegisterAgentRequest.
type RegisterRequest struct {
	ID           string `json:"id"`
	Hostname     string `json:"hostname"`
	IPAddress    string `json:"ipAddress"`
	OSVersion    string `json:"osVersion"`
	AgentVersion string `json:"agentVersion"`
	Port         int    `json:"port"`
}

// Client handles agent registration with the central server.
type Client struct {
	serverURL string
	port      int
	logger    *slog.Logger
	client    *http.Client
}

// NewClient creates a new enrollment client.
// serverURL is the base URL of the central server (e.g., "https://server.campus.edu:8080").
func NewClient(serverURL string, agentPort int, logger *slog.Logger) *Client {
	return &Client{
		serverURL: serverURL,
		port:      agentPort,
		logger:    logger,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Register sends a registration/heartbeat to the central server.
func (c *Client) Register(ctx context.Context) error {
	hostname, _ := os.Hostname()
	ip := getOutboundIP()

	req := RegisterRequest{
		ID:           hostname,
		Hostname:     hostname,
		IPAddress:    ip,
		OSVersion:    "", // TODO: read from registry
		AgentVersion: agentVersion,
		Port:         c.port,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal registration: %w", err)
	}

	url := c.serverURL + "/api/v1/agents/register"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("send registration: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registration failed: status %d", resp.StatusCode)
	}

	c.logger.Info("registered with server", "url", url, "hostname", hostname)
	return nil
}

// RunHeartbeat periodically registers with the server.
// Blocks until ctx is cancelled.
func (c *Client) RunHeartbeat(ctx context.Context, interval time.Duration) {
	// Register immediately on startup.
	if err := c.Register(ctx); err != nil {
		c.logger.Warn("initial registration failed (will retry)", "error", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Register(ctx); err != nil {
				c.logger.Warn("heartbeat failed", "error", err)
			}
		}
	}
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
