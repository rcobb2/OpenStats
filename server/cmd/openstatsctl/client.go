package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// defaultServer is used when neither --server nor OPENSTATS_URL is set.
const defaultServer = "https://openstats.colgate.edu"

// client talks to one OpenStats instance. The API has no auth — it is designed
// for internal campus networks — so there is no credential handling here.
type client struct {
	baseURL string
	http    *http.Client
}

func newClient(server string) *client {
	if server == "" {
		server = os.Getenv("OPENSTATS_URL")
	}
	if server == "" {
		server = defaultServer
	}
	return &client{
		baseURL: strings.TrimRight(server, "/"),
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// get fetches path (relative to /api/v1) and decodes it into out. Pass a
// *json.RawMessage to keep the payload undecoded.
func (c *client) get(path string, params url.Values, out interface{}) error {
	endpoint := c.baseURL + "/api/v1" + path
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}

	resp, err := c.http.Get(endpoint)
	if err != nil {
		return fmt.Errorf("GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", endpoint, describeError(resp.StatusCode, body))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response from %s: %w", endpoint, err)
	}
	return nil
}

// send performs a write request. Every mutating command routes through here so
// that the target server is always reported — an agent editing the wrong
// instance should be obvious in the transcript.
func (c *client) send(method, path string, payload interface{}) error {
	endpoint := c.baseURL + "/api/v1" + path

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode payload: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, endpoint, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s %s: %s", method, endpoint, describeError(resp.StatusCode, raw))
	}
	return nil
}

// describeError turns an error response into something a human or an agent can
// act on, preferring the server's own {"error": "..."} message over the status.
func describeError(status int, body []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error != "" {
		return fmt.Sprintf("HTTP %d: %s", status, payload.Error)
	}
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) > 200 {
		trimmed = trimmed[:200] + "…"
	}
	if trimmed == "" {
		return fmt.Sprintf("HTTP %d", status)
	}
	return fmt.Sprintf("HTTP %d: %s", status, trimmed)
}

// --- API payloads ---
//
// These are declared locally rather than imported from internal/store so the
// CLI stays decoupled from the database schema and free of a Postgres driver.

type agent struct {
	ID            string    `json:"id"`
	Hostname      string    `json:"hostname"`
	IPAddress     string    `json:"ipAddress"`
	OSVersion     string    `json:"osVersion"`
	AgentVersion  string    `json:"agentVersion"`
	LabID         *string   `json:"labId"`
	Port          int       `json:"port"`
	Status        string    `json:"status"`
	PendingUpdate string    `json:"pendingUpdate"`
	Building      string    `json:"building"`
	Room          string    `json:"room"`
	LastSeen      time.Time `json:"lastSeen"`
}

type lab struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Building    string `json:"building"`
	Room        string `json:"room"`
	Description string `json:"description"`
}

type softwareMapping struct {
	ID          int    `json:"id"`
	ExeName     string `json:"exeName"`
	DisplayName string `json:"displayName"`
	Category    string `json:"category"`
	Publisher   string `json:"publisher"`
	Family      string `json:"family"`
	Source      string `json:"source"`
	Ignored     bool   `json:"ignored"`
}

type discoveredUser struct {
	CanonicalUser string   `json:"canonicalUser"`
	DisplayName   string   `json:"displayName"`
	RawUsers      []string `json:"rawUsers"`
	Ignored       bool     `json:"ignored"`
	MatchedRule   string   `json:"matchedRule"`
	RuleID        int      `json:"ruleId"`
	ActiveNow     bool     `json:"activeNow"`
	SessionHours  float64  `json:"sessionHours"`
}

type userMapping struct {
	ID            int    `json:"id"`
	Pattern       string `json:"pattern"`
	CanonicalUser string `json:"canonicalUser"`
	DisplayName   string `json:"displayName"`
	Notes         string `json:"notes"`
	Source        string `json:"source"`
	Ignored       bool   `json:"ignored"`
}

type userPolicy struct {
	StripDomain    bool              `json:"stripDomain"`
	IgnorePatterns []string          `json:"ignorePatterns"`
	Aliases        map[string]string `json:"aliases"`
}

type summary struct {
	TotalAgents   int `json:"totalAgents"`
	OnlineAgents  int `json:"onlineAgents"`
	TotalLabs     int `json:"totalLabs"`
	TotalMappings int `json:"totalMappings"`
}

type buildInfo struct {
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
}

type settings struct {
	HeartbeatIntervalSeconds int    `json:"heartbeatIntervalSeconds"`
	UpdateIntervalSeconds    int    `json:"updateIntervalSeconds"`
	StaleTimeoutDays         int    `json:"staleTimeoutDays"`
	MinAgentVersion          string `json:"minAgentVersion"`
	MaintenanceWindowStart   string `json:"maintenanceWindowStart"`
	MaintenanceWindowEnd     string `json:"maintenanceWindowEnd"`
}

// promVector is the instant-vector shape every report endpoint returns.
type promVector struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
}
