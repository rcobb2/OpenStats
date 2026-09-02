package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the full server configuration.
type Config struct {
	Server        ServerConfig        `yaml:"server"`
	Database      DatabaseConfig      `yaml:"database"`
	Prom          PromConfig          `yaml:"prometheus"`
	FileSD        FileSDConfig        `yaml:"fileSD"`
	InstallerSync InstallerSyncConfig `yaml:"installerSync"`
}

// InstallerSyncConfig controls the background poller that keeps the local
// installers directory in sync with the latest GitHub release, so newly
// published agent builds become available for the staggered auto-update rollout
// without a manual fetch step.
type InstallerSyncConfig struct {
	// Enabled is a pointer so an omitted config key defaults to on (set in
	// setDefaults); set `enabled: false` explicitly to disable the poller.
	Enabled         *bool  `yaml:"enabled"`
	GitHubRepo      string `yaml:"githubRepo"`
	IntervalMinutes int    `yaml:"intervalMinutes"`
}

type ServerConfig struct {
	Port      int    `yaml:"port"`
	Host      string `yaml:"host"`
	PublicDir string `yaml:"publicDir"`
}

type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbName"`
	SSLMode  string `yaml:"sslMode"`
}

func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		d.User, d.Password, d.Host, d.Port, d.DBName, d.SSLMode,
	)
}

type PromConfig struct {
	URL string `yaml:"url"`
}

type FileSDConfig struct {
	OutputPath string `yaml:"outputPath"`
}

// Load reads and parses the config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	setDefaults(cfg)
	applyEnvOverrides(cfg)
	return cfg, nil
}

// applyEnvOverrides allows deployment-time overrides without editing server.yaml.
// Environment variables take precedence over config file values.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("PROMETHEUS_URL"); v != "" {
		cfg.Prom.URL = v
	}
	if v := os.Getenv("DB_PASSWORD"); v != "" {
		cfg.Database.Password = v
	}
}

func setDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.PublicDir == "" {
		cfg.Server.PublicDir = "public"
	}
	if cfg.Database.Host == "" {
		cfg.Database.Host = "localhost"
	}
	if cfg.Database.Port == 0 {
		cfg.Database.Port = 5432
	}
	if cfg.Database.User == "" {
		cfg.Database.User = "openlabstats"
	}
	if cfg.Database.DBName == "" {
		cfg.Database.DBName = "openlabstats"
	}
	if cfg.Database.SSLMode == "" {
		cfg.Database.SSLMode = "disable"
	}
	if cfg.Prom.URL == "" {
		cfg.Prom.URL = "http://prometheus:9090"
	}
	if cfg.FileSD.OutputPath == "" {
		cfg.FileSD.OutputPath = "/etc/prometheus/file_sd/openlabstats.json"
	}
	if cfg.InstallerSync.GitHubRepo == "" {
		cfg.InstallerSync.GitHubRepo = "rcobb2/OpenStats"
	}
	if cfg.InstallerSync.IntervalMinutes == 0 {
		cfg.InstallerSync.IntervalMinutes = 30
	}
	if cfg.InstallerSync.Enabled == nil {
		enabled := true
		cfg.InstallerSync.Enabled = &enabled
	}
}
