package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps a PostgreSQL connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a new store and runs migrations.
func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Close shuts down the connection pool.
func (s *Store) Close() {
	s.pool.Close()
}

// migrate creates the database schema.
func (s *Store) migrate(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS labs (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		building    TEXT NOT NULL DEFAULT '',
		room        TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS agents (
		id          TEXT PRIMARY KEY,
		hostname    TEXT NOT NULL,
		ip_address  TEXT NOT NULL DEFAULT '',
		os_version  TEXT NOT NULL DEFAULT '',
		agent_version TEXT NOT NULL DEFAULT '',
		lab_id      TEXT REFERENCES labs(id) ON DELETE SET NULL,
		port        INT NOT NULL DEFAULT 9183,
		status      TEXT NOT NULL DEFAULT 'unknown',
		last_seen   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS software_mappings (
		id           SERIAL PRIMARY KEY,
		exe_name     TEXT NOT NULL UNIQUE,
		display_name TEXT NOT NULL,
		category     TEXT NOT NULL DEFAULT 'Unknown',
		publisher    TEXT NOT NULL DEFAULT 'Unknown',
		family       TEXT NOT NULL DEFAULT '',
		source       TEXT NOT NULL DEFAULT 'manual',
		created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS installer_builds (
		id             SERIAL PRIMARY KEY,
		server_address TEXT NOT NULL,
		port           INT NOT NULL DEFAULT 9183,
		version        TEXT NOT NULL,
		filename       TEXT NOT NULL,
		created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_agents_lab ON agents(lab_id);
	CREATE INDEX IF NOT EXISTS idx_agents_status ON agents(status);
	CREATE INDEX IF NOT EXISTS idx_mappings_exe ON software_mappings(exe_name);

	ALTER TABLE labs ADD COLUMN IF NOT EXISTS room TEXT NOT NULL DEFAULT '';
	`
	_, err := s.pool.Exec(ctx, schema)
	if err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// --- Agent operations ---

type Agent struct {
	ID           string    `json:"id"`
	Hostname     string    `json:"hostname"`
	IPAddress    string    `json:"ipAddress"`
	OSVersion    string    `json:"osVersion"`
	AgentVersion string    `json:"agentVersion"`
	LabID        *string   `json:"labId,omitempty"`
	Port         int       `json:"port"`
	Status       string    `json:"status"`
	LastSeen     time.Time `json:"lastSeen"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// UpsertAgent registers or updates an agent (idempotent on hostname).
func (s *Store) UpsertAgent(ctx context.Context, a *Agent) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agents (id, hostname, ip_address, os_version, agent_version, port, status, last_seen)
		VALUES ($1, $2, $3, $4, $5, $6, 'online', NOW())
		ON CONFLICT (id) DO UPDATE SET
			ip_address = EXCLUDED.ip_address,
			os_version = EXCLUDED.os_version,
			agent_version = EXCLUDED.agent_version,
			port = EXCLUDED.port,
			status = 'online',
			last_seen = NOW(),
			updated_at = NOW()`,
		a.ID, a.Hostname, a.IPAddress, a.OSVersion, a.AgentVersion, a.Port,
	)
	return err
}

// ListAgents returns all enrolled agents.
func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, hostname, ip_address, os_version, agent_version, lab_id, port, status, last_seen, created_at, updated_at
		FROM agents ORDER BY hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.Hostname, &a.IPAddress, &a.OSVersion, &a.AgentVersion, &a.LabID, &a.Port, &a.Status, &a.LastSeen, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// GetAgent returns a single agent by ID.
func (s *Store) GetAgent(ctx context.Context, id string) (*Agent, error) {
	a := &Agent{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, hostname, ip_address, os_version, agent_version, lab_id, port, status, last_seen, created_at, updated_at
		FROM agents WHERE id = $1`, id).
		Scan(&a.ID, &a.Hostname, &a.IPAddress, &a.OSVersion, &a.AgentVersion, &a.LabID, &a.Port, &a.Status, &a.LastSeen, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// AssignAgentToLab assigns an agent to a lab.
func (s *Store) AssignAgentToLab(ctx context.Context, agentID, labID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE agents SET lab_id = $1, updated_at = NOW() WHERE id = $2`, labID, agentID)
	return err
}

// DeleteAgent removes an agent.
func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM agents WHERE id = $1`, id)
	return err
}

// MarkStaleAgents sets agents that haven't checked in recently to 'offline'.
func (s *Store) MarkStaleAgents(ctx context.Context, threshold time.Duration) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE agents SET status = 'offline', updated_at = NOW()
		WHERE status = 'online' AND last_seen < NOW() - $1::interval`,
		threshold.String(),
	)
	return err
}

// --- Lab operations ---

type Lab struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Building    string    `json:"building"`
	Room        string    `json:"room"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (s *Store) CreateLab(ctx context.Context, l *Lab) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO labs (id, name, building, room, description)
		VALUES ($1, $2, $3, $4, $5)`,
		l.ID, l.Name, l.Building, l.Room, l.Description,
	)
	return err
}

func (s *Store) ListLabs(ctx context.Context) ([]Lab, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, building, room, description, created_at, updated_at FROM labs ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var labs []Lab
	for rows.Next() {
		var l Lab
		if err := rows.Scan(&l.ID, &l.Name, &l.Building, &l.Room, &l.Description, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, err
		}
		labs = append(labs, l)
	}
	return labs, rows.Err()
}

func (s *Store) GetLab(ctx context.Context, id string) (*Lab, error) {
	l := &Lab{}
	err := s.pool.QueryRow(ctx, `SELECT id, name, building, room, description, created_at, updated_at FROM labs WHERE id = $1`, id).
		Scan(&l.ID, &l.Name, &l.Building, &l.Room, &l.Description, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return l, nil
}

func (s *Store) UpdateLab(ctx context.Context, l *Lab) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE labs SET name = $1, building = $2, room = $3, description = $4, updated_at = NOW()
		WHERE id = $5`,
		l.Name, l.Building, l.Room, l.Description, l.ID,
	)
	return err
}

func (s *Store) DeleteLab(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM labs WHERE id = $1`, id)
	return err
}

// --- Software mapping operations ---

type SoftwareMapping struct {
	ID          int       `json:"id"`
	ExeName     string    `json:"exeName"`
	DisplayName string    `json:"displayName"`
	Category    string    `json:"category"`
	Publisher   string    `json:"publisher"`
	Family      string    `json:"family"`
	Source      string    `json:"source"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (s *Store) ListMappings(ctx context.Context) ([]SoftwareMapping, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, exe_name, display_name, category, publisher, family, source, created_at, updated_at
		FROM software_mappings ORDER BY exe_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mappings []SoftwareMapping
	for rows.Next() {
		var m SoftwareMapping
		if err := rows.Scan(&m.ID, &m.ExeName, &m.DisplayName, &m.Category, &m.Publisher, &m.Family, &m.Source, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		mappings = append(mappings, m)
	}
	return mappings, rows.Err()
}

func (s *Store) UpsertMapping(ctx context.Context, m *SoftwareMapping) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO software_mappings (exe_name, display_name, category, publisher, family, source)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (exe_name) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			category = EXCLUDED.category,
			publisher = EXCLUDED.publisher,
			family = EXCLUDED.family,
			source = EXCLUDED.source,
			updated_at = NOW()`,
		m.ExeName, m.DisplayName, m.Category, m.Publisher, m.Family, m.Source,
	)
	return err
}

func (s *Store) DeleteMapping(ctx context.Context, id int) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM software_mappings WHERE id = $1`, id)
	return err
}

// GetMappingsAsAgentJSON returns mappings in the format agents expect (software-map.json style).
func (s *Store) GetMappingsAsAgentJSON(ctx context.Context) (map[string]interface{}, error) {
	mappings, err := s.ListMappings(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]interface{})
	entries := make(map[string]map[string]string)
	for _, m := range mappings {
		entry := map[string]string{
			"displayName": m.DisplayName,
			"category":    m.Category,
			"publisher":   m.Publisher,
		}
		if m.Family != "" {
			entry["family"] = m.Family
		}
		entries[m.ExeName] = entry
	}
	result["_version"] = "server-managed"
	result["mappings"] = entries
	return result, nil
}

// --- Prometheus target helpers ---

// AgentTarget is a Prometheus file_sd_configs target entry.
type AgentTarget struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

// GetPrometheusTargets returns all online agents formatted for file_sd_configs.
func (s *Store) GetPrometheusTargets(ctx context.Context) ([]AgentTarget, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.hostname, a.ip_address, a.port, COALESCE(l.name, '') as lab_name, COALESCE(l.building, '') as building, COALESCE(l.room, '') as room
		FROM agents a
		LEFT JOIN labs l ON a.lab_id = l.id
		WHERE a.status = 'online'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []AgentTarget
	for rows.Next() {
		var hostname, ip string
		var port int
		var labName, building, room string
		if err := rows.Scan(&hostname, &ip, &port, &labName, &building, &room); err != nil {
			return nil, err
		}

		// Prefer IP if available, fall back to hostname.
		addr := hostname
		if ip != "" {
			addr = ip
		}
		target := fmt.Sprintf("%s:%d", addr, port)

		labels := map[string]string{
			"hostname": hostname,
		}
		if labName != "" {
			labels["lab"] = labName
		}
		if building != "" {
			labels["building"] = building
		}
		if room != "" {
			labels["room"] = room
		}

		targets = append(targets, AgentTarget{
			Targets: []string{target},
			Labels:  labels,
		})
	}
	return targets, rows.Err()
}
