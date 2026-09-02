package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store provides local SQLite persistence for process sessions.
type Store struct {
	db     *sql.DB
	logger *slog.Logger
}

// New opens or creates a SQLite database at the given path.
func New(dbPath string, logger *slog.Logger) (*Store, error) {
	// Ensure the directory exists.
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable WAL mode for better concurrent read/write performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set WAL mode: %w", err)
	}

	s := &Store{db: db, logger: logger}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}

	logger.Info("database opened", "path", dbPath)
	return s, nil
}

// migrate creates the database schema if it doesn't exist.
func (s *Store) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS process_sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		pid INTEGER NOT NULL,
		exe_name TEXT NOT NULL,
		exe_path TEXT,
		display_name TEXT,
		category TEXT,
		publisher TEXT,
		user TEXT,
		hostname TEXT,
		start_time DATETIME NOT NULL,
		stop_time DATETIME,
		duration_seconds REAL,
		foreground_seconds REAL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_sessions_start ON process_sessions(start_time);
	CREATE INDEX IF NOT EXISTS idx_sessions_exe ON process_sessions(exe_name);
	CREATE INDEX IF NOT EXISTS idx_sessions_user ON process_sessions(user);

	CREATE TABLE IF NOT EXISTS app_usage_totals (
		exe_name TEXT NOT NULL,
		display_name TEXT,
		category TEXT,
		user TEXT,
		hostname TEXT,
		total_seconds REAL NOT NULL DEFAULT 0,
		total_foreground_seconds REAL NOT NULL DEFAULT 0,
		total_launches INTEGER NOT NULL DEFAULT 0,
		total_elevations INTEGER NOT NULL DEFAULT 0,
		last_updated DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (exe_name, user, hostname)
	);
	`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	// Add new columns for existing databases; ignore "duplicate column" from older schemas.
	for _, stmt := range []string{
		"ALTER TABLE process_sessions ADD COLUMN foreground_seconds REAL DEFAULT 0;",
		"ALTER TABLE app_usage_totals ADD COLUMN total_foreground_seconds REAL DEFAULT 0;",
		"ALTER TABLE app_usage_totals ADD COLUMN total_elevations INTEGER NOT NULL DEFAULT 0;",
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			s.logger.Warn("schema migration warning", "stmt", stmt, "err", err)
		}
	}

	s.logger.Info("database schema ready")
	return nil
}

// RecordSession stores a completed process session.
func (s *Store) RecordSession(
	pid uint32, exeName, exePath, displayName, category, publisher, user, hostname string,
	startTime, stopTime time.Time, foregroundSeconds float64,
) error {
	startTime = startTime.UTC()
	stopTime = stopTime.UTC()
	duration := stopTime.Sub(startTime).Seconds()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert the session record.
	_, err = tx.Exec(`
		INSERT INTO process_sessions 
			(pid, exe_name, exe_path, display_name, category, publisher, user, hostname, start_time, stop_time, duration_seconds, foreground_seconds)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pid, exeName, exePath, displayName, category, publisher, user, hostname, startTime, stopTime, duration, foregroundSeconds,
	)
	if err != nil {
		return fmt.Errorf("failed to insert session: %w", err)
	}

	// Update the running totals.
	_, err = tx.Exec(`
		INSERT INTO app_usage_totals (exe_name, display_name, category, user, hostname, total_seconds, total_foreground_seconds, total_launches, last_updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(exe_name, user, hostname) DO UPDATE SET
			total_seconds = total_seconds + ?,
			total_foreground_seconds = total_foreground_seconds + ?,
			total_launches = total_launches + 1,
			display_name = ?,
			category = ?,
			last_updated = CURRENT_TIMESTAMP`,
		exeName, displayName, category, user, hostname, duration, foregroundSeconds,
		duration, foregroundSeconds, displayName, category,
	)
	if err != nil {
		return fmt.Errorf("failed to update totals: %w", err)
	}

	return tx.Commit()
}

// RecordElevation bumps the elevation total for an exe/user/host. Elevations
// are counted at process start (a cancelled UAC prompt never creates a
// process; a short-lived elevated installer must still count), so they can't
// ride along in RecordSession. Deliberately does not overwrite display_name or
// category on conflict — RecordSession owns those.
func (s *Store) RecordElevation(exeName, displayName, category, user, hostname string) error {
	_, err := s.db.Exec(`
		INSERT INTO app_usage_totals (exe_name, display_name, category, user, hostname, total_seconds, total_foreground_seconds, total_launches, total_elevations, last_updated)
		VALUES (?, ?, ?, ?, ?, 0, 0, 0, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(exe_name, user, hostname) DO UPDATE SET
			total_elevations = total_elevations + 1,
			last_updated = CURRENT_TIMESTAMP`,
		exeName, displayName, category, user, hostname,
	)
	if err != nil {
		return fmt.Errorf("failed to record elevation: %w", err)
	}
	return nil
}

// GetUsageTotals returns all accumulated usage totals (for restoring metrics on restart).
type UsageTotal struct {
	ExeName                string
	DisplayName            string
	Category               string
	User                   string
	Hostname               string
	TotalSeconds           float64
	TotalForegroundSeconds float64
	TotalLaunches          int64
	TotalElevations        int64
}

func (s *Store) GetUsageTotals() ([]UsageTotal, error) {
	rows, err := s.db.Query(`
		SELECT exe_name, display_name, category, user, hostname, total_seconds, total_foreground_seconds, total_launches, total_elevations
		FROM app_usage_totals`)
	if err != nil {
		return nil, fmt.Errorf("failed to query totals: %w", err)
	}
	defer rows.Close()

	var totals []UsageTotal
	for rows.Next() {
		var t UsageTotal
		// coalesce columns that may be null in databases created before they existed
		var fg sql.NullFloat64
		var elev sql.NullInt64
		if err := rows.Scan(&t.ExeName, &t.DisplayName, &t.Category, &t.User, &t.Hostname, &t.TotalSeconds, &fg, &t.TotalLaunches, &elev); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		t.TotalForegroundSeconds = fg.Float64
		t.TotalElevations = elev.Int64
		totals = append(totals, t)
	}
	return totals, rows.Err()
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
