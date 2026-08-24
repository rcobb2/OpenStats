//go:build windows

package config

import (
	"golang.org/x/sys/windows/registry"

	"github.com/rcobb/openlabstats-agent/internal/urlutil"
)

// RegistryKeyPath is where the MSI records the values passed to msiexec
// (SERVERADDRESS, PORT, BUILDING, ROOM). The installer writes these with native
// WiX RegistryValue elements, which are transactional and roll back on failure.
//
// This replaced a deferred PowerShell custom action that regex-patched
// agent.yaml in place with Return="ignore". If that action failed for any reason
// — AppLocker, endpoint security, constrained language mode — the MSI still
// reported success and left reportURL empty, so the agent installed, started,
// and silently never registered.
const RegistryKeyPath = `SOFTWARE\OpenLabStats`

// applyPlatformOverrides layers installer-supplied registry values over the
// values parsed from agent.yaml. A non-empty registry value wins: the MSI is the
// source of truth for fleet-wide configuration, and agent.yaml ships with empty
// defaults precisely so these can fill them in.
//
// A missing key is not an error — that's the normal case for a hand-configured
// or non-MSI install, where agent.yaml alone is authoritative.
func applyPlatformOverrides(cfg *Config) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, RegistryKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return
	}
	defer key.Close()

	// Normalize here as well as in enrollment: the MSI records whatever the
	// admin typed, and a scheme-less SERVERADDRESS would otherwise also be
	// inherited by the derived mapping URL, which has no normalization of its
	// own. NormalizeServerURL is idempotent.
	if v, _, err := key.GetStringValue("ServerAddress"); err == nil && v != "" {
		cfg.Server.ReportURL = urlutil.NormalizeServerURL(v)
	}
	if v, _, err := key.GetStringValue("MappingUpdateURL"); err == nil && v != "" {
		cfg.Normalizer.MappingUpdateURL = urlutil.NormalizeServerURL(v)
	}
	if v, _, err := key.GetStringValue("Building"); err == nil && v != "" {
		cfg.Monitor.Building = v
	}
	if v, _, err := key.GetStringValue("Room"); err == nil && v != "" {
		cfg.Monitor.Room = v
	}

	// Port is written as a string by the MSI (RegistryValue Type="string") so
	// that an unset property yields "" rather than 0, which would be
	// indistinguishable from a deliberate value.
	if v, _, err := key.GetStringValue("Port"); err == nil && v != "" {
		if p, ok := parsePort(v); ok {
			cfg.Server.Port = p
		}
	}
}
