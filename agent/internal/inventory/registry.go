//go:build windows

package inventory

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Scan reads installed software from the standard Uninstall registry keys.
// It checks both 64-bit and 32-bit registry views.
func (s *Scanner) Scan() []InstalledApp {
	var apps []InstalledApp

	// 64-bit applications.
	apps = append(apps, s.scanKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`)...)
	// 32-bit applications (WoW64).
	apps = append(apps, s.scanKey(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`)...)
	// Per-user applications.
	apps = append(apps, s.scanKey(registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`)...)

	// Deduplicate by name+version.
	seen := make(map[string]bool)
	var unique []InstalledApp
	for _, app := range apps {
		key := strings.ToLower(app.Name + "|" + app.Version)
		if app.Name != "" && !seen[key] {
			seen[key] = true
			unique = append(unique, app)
		}
	}

	s.logger.Info("inventory scan complete", "count", len(unique))
	return unique
}

func (s *Scanner) scanKey(root registry.Key, path string) []InstalledApp {
	key, err := registry.OpenKey(root, path, registry.READ)
	if err != nil {
		s.logger.Debug("failed to open registry key", "path", path, "error", err)
		return nil
	}
	defer key.Close()

	subkeys, err := key.ReadSubKeyNames(-1)
	if err != nil {
		s.logger.Debug("failed to read subkeys", "path", path, "error", err)
		return nil
	}

	var apps []InstalledApp
	for _, subkeyName := range subkeys {
		subkey, err := registry.OpenKey(key, subkeyName, registry.READ)
		if err != nil {
			continue
		}

		name, _, _ := subkey.GetStringValue("DisplayName")
		version, _, _ := subkey.GetStringValue("DisplayVersion")
		publisher, _, _ := subkey.GetStringValue("Publisher")
		systemComponent, _, _ := subkey.GetIntegerValue("SystemComponent")
		subkey.Close()

		// Skip system components and entries without display names.
		if name == "" || systemComponent == 1 {
			continue
		}

		apps = append(apps, InstalledApp{
			Name:      name,
			Version:   version,
			Publisher: publisher,
		})
	}

	return apps
}
