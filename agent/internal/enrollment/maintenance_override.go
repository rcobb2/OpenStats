package enrollment

import (
	"os"
	"path/filepath"
	"strings"
)

// maintenanceOverrideFileName is the marker file written by the
// `setmaintenance` CLI command and read back both by the CLI's own status
// commands and by the actual running agent service. Living in the install's
// base directory (the same root config.Load resolves Store.DBPath against)
// keeps it next to the agent's other local state rather than inventing a new
// convention.
const maintenanceOverrideFileName = "maintenance_override"

// ReadMaintenanceOverride returns the persisted override for baseDir, or nil
// if none is set (file absent, empty, or unrecognized content — all treated
// as "no override" rather than an error, since a corrupt marker should never
// be able to silently force one state or the other).
func ReadMaintenanceOverride(baseDir string) *bool {
	if baseDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(baseDir, maintenanceOverrideFileName))
	if err != nil {
		return nil
	}
	switch strings.TrimSpace(string(data)) {
	case "on":
		v := true
		return &v
	case "off":
		v := false
		return &v
	default:
		return nil
	}
}

// WriteMaintenanceOverride persists override to baseDir — "on"/"off" for a
// forced value, or removes the marker entirely for nil ("auto", time-based).
// Removing rather than writing a third "auto" sentinel means a leftover file
// from an older agent version can never be misread as a forced value by a
// newer one, or vice versa.
func WriteMaintenanceOverride(baseDir string, override *bool) error {
	path := filepath.Join(baseDir, maintenanceOverrideFileName)
	if override == nil {
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	val := "off"
	if *override {
		val = "on"
	}
	return os.WriteFile(path, []byte(val), 0o644)
}
