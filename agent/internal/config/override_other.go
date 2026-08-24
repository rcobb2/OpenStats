//go:build !windows

package config

// applyPlatformOverrides is a no-op off Windows. macOS installs are configured
// by the .pkg's postinstall script writing agent.yaml directly, so there is no
// second configuration source to layer in.
func applyPlatformOverrides(_ *Config) {}
