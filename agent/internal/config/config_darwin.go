//go:build darwin

package config

// setPlatformDefaults sets macOS-specific configuration defaults.
// Called by setDefaults after shared defaults are applied.
func setPlatformDefaults(cfg *Config) {
	if len(cfg.Monitor.ExcludePatterns) > 0 {
		return // user has configured their own patterns; don't override
	}

	// NOTE: System processes under /System/, /usr/bin, /usr/sbin/, /usr/libexec/,
	// /bin/, /sbin/ are excluded automatically by path in proc_darwin.go.
	// These patterns cover non-system-path processes we still want to ignore.
	cfg.Monitor.ExcludePatterns = []string{
		// Desktop shell (run from /System/ but also matched by name for safety)
		"^WindowServer$",
		"^loginwindow$",
		"^Dock$",
		"^Finder$",
		"^SystemUIServer$",
		"^ControlCenter$",
		"^NotificationCenter$",
		"^Spotlight$",

		// Third-party background services (not user-facing lab software)
		// Adobe CC sync daemon (not itself a user app)
		"^AdobeIPCBroker$",
		"^AdobeCRDaemon$",
		// Google Update
		"(?i)^GoogleSoftwareUpdate",
		// Microsoft MAU
		"(?i)^Microsoft AutoUpdate$",

		// Shells and interpreters
		"^bash$", "^zsh$", "^sh$", "^fish$", "^tcsh$", "^ksh$",
		"(?i)^python[0-9.]*$",
		"(?i)^perl[0-9.]*$",
		"(?i)^ruby[0-9.]*$",
		"^node$",

		// Common CLI tools
		"^caffeinate$",
		"^sleep$",
		"^ssh$",
		"^git$",
		"^curl$",
		"^vim$",
		"^nvim$",
		"^nano$",
		"^emacs$",

		// Electron / Chromium subprocess noise — Helper processes spawned by
		// apps like VS Code, Edge, etc. pbi_name is capped at 31 chars, so
		// "...Helper (Renderer)" may arrive truncated; match the prefix only.
		"(?i).* Helper \\(",
		"(?i).*_crashpad_handler$",
		"(?i)^pasteboard\\.xpc$",

		// The agent itself
		"^openlabstats-agent$",
	}
}
