package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePort(t *testing.T) {
	tests := []struct {
		in   string
		want int
		ok   bool
	}{
		{"9183", 9183, true},
		{"1", 1, true},
		{"65535", 65535, true},
		{"  9183  ", 9183, true},
		// An unset MSI property formats to "" — must not become port 0.
		{"", 0, false},
		{"0", 0, false},
		{"65536", 0, false},
		{"-1", 0, false},
		{"[PORT]", 0, false},
		{"abc", 0, false},
	}
	for _, tt := range tests {
		got, ok := parsePort(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("parsePort(%q) = (%d, %v), want (%d, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// Regression: BaseDir must resolve to the same install root Store.DBPath and
// friends are already resolved against (one level up from the config file's
// own directory) — it's the one place local agent state that isn't really
// "config" (the setmaintenance override marker) can live without inventing
// its own path convention.
func TestLoadSetsBaseDirToInstallRoot(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "agent.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  port: 9183\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BaseDir != root {
		t.Errorf("BaseDir = %q, want %q", cfg.BaseDir, root)
	}
}
