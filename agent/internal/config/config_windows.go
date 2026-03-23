//go:build windows

package config

// setPlatformDefaults is a no-op on Windows; exclude patterns are set via the config file.
func setPlatformDefaults(cfg *Config) {}
