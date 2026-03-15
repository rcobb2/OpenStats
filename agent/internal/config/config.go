package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the full agent configuration.
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Monitor    MonitorConfig    `yaml:"monitor"`
	Normalizer NormalizerConfig `yaml:"normalizer"`
	Inventory  InventoryConfig  `yaml:"inventory"`
	Store      StoreConfig      `yaml:"store"`
	Logging    LoggingConfig    `yaml:"logging"`
}

type ServerConfig struct {
	Port        int    `yaml:"port"`
	MetricsPath string `yaml:"metricsPath"`
	ReportURL   string `yaml:"reportURL"`
}

type MonitorConfig struct {
	ReconcileInterval time.Duration `yaml:"reconcileInterval"`
	MinLifetime       time.Duration `yaml:"minLifetime"`
	ExcludePatterns   []string      `yaml:"excludePatterns"`
}

type NormalizerConfig struct {
	MappingFile            string        `yaml:"mappingFile"`
	MappingUpdateURL       string        `yaml:"mappingUpdateURL"`
	MappingRefreshInterval time.Duration `yaml:"mappingRefreshInterval"`
}

type InventoryConfig struct {
	ScanInterval time.Duration `yaml:"scanInterval"`
}

type StoreConfig struct {
	DBPath string `yaml:"dbPath"`
}

type LoggingConfig struct {
	Level    string `yaml:"level"`
	FilePath string `yaml:"filePath"`
}

// Load reads and parses the config file at the given path.
// If configPath is empty, it searches for configs/agent.yaml relative to the executable.
// Relative paths in the config are resolved relative to the config file's directory.
func Load(configPath string) (*Config, error) {
	if configPath == "" {
		exePath, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("failed to determine executable path: %w", err)
		}
		configPath = filepath.Join(filepath.Dir(exePath), "configs", "agent.yaml")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	setDefaults(cfg)

	// Resolve relative paths relative to the config file's directory.
	// This ensures paths work correctly when running as a Windows service
	// (where the working directory is typically C:\Windows\System32).
	configDir := filepath.Dir(configPath)
	baseDir := filepath.Dir(configDir) // Go up from configs/ to install root
	cfg.Store.DBPath = resolvePath(baseDir, cfg.Store.DBPath)
	cfg.Logging.FilePath = resolvePath(baseDir, cfg.Logging.FilePath)
	cfg.Normalizer.MappingFile = resolvePath(baseDir, cfg.Normalizer.MappingFile)

	return cfg, nil
}

// resolvePath makes a relative path absolute by joining it with baseDir.
func resolvePath(baseDir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(baseDir, p)
}

func setDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 9183
	}
	if cfg.Server.MetricsPath == "" {
		cfg.Server.MetricsPath = "/metrics"
	}
	if cfg.Monitor.ReconcileInterval == 0 {
		cfg.Monitor.ReconcileInterval = 30 * time.Second
	}
	if cfg.Monitor.MinLifetime == 0 {
		cfg.Monitor.MinLifetime = 2 * time.Second
	}
	if cfg.Normalizer.MappingFile == "" {
		cfg.Normalizer.MappingFile = "configs/software-map.json"
	}
	if cfg.Normalizer.MappingRefreshInterval == 0 {
		cfg.Normalizer.MappingRefreshInterval = 1 * time.Hour
	}
	if cfg.Inventory.ScanInterval == 0 {
		cfg.Inventory.ScanInterval = 1 * time.Hour
	}
	if cfg.Store.DBPath == "" {
		cfg.Store.DBPath = "data/openlabstats.db"
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.FilePath == "" {
		cfg.Logging.FilePath = "logs/agent.log"
	}
}
