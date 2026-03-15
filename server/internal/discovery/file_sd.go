package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/rcobb/openlabstats-server/internal/store"
)

// FileSD manages Prometheus file_sd_configs target files.
// When agents register or are removed, it rewrites the JSON file
// so Prometheus automatically picks up the new targets.
type FileSD struct {
	mu         sync.Mutex
	outputPath string
	logger     *slog.Logger
}

// NewFileSD creates a new file service discovery writer.
func NewFileSD(outputPath string, logger *slog.Logger) *FileSD {
	return &FileSD{
		outputPath: outputPath,
		logger:     logger,
	}
}

// Refresh queries the store for all online agents and writes the
// Prometheus file_sd_configs JSON file.
func (f *FileSD) Refresh(ctx context.Context, st *store.Store) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	targets, err := st.GetPrometheusTargets(ctx)
	if err != nil {
		return fmt.Errorf("get targets: %w", err)
	}

	// Prometheus expects an empty array if no targets, not null.
	if targets == nil {
		targets = []store.AgentTarget{}
	}

	data, err := json.MarshalIndent(targets, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal targets: %w", err)
	}

	// Ensure output directory exists.
	dir := filepath.Dir(f.outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	// Write atomically: write to temp file, then rename.
	tmpPath := f.outputPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, f.outputPath); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}

	f.logger.Info("prometheus targets updated", "path", f.outputPath, "targets", len(targets))
	return nil
}
