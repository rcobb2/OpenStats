package normalizer

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// mappingFileData is the JSON structure of the software-map.json file.
type mappingFileData struct {
	Version  string              `json:"_version"`
	Mappings map[string]*AppInfo `json:"mappings"`
}

// MappingFile manages the community/server-curated software name mapping.
type MappingFile struct {
	mu       sync.RWMutex
	filePath string
	mappings map[string]*AppInfo // lowercase exe name -> AppInfo
	logger   *slog.Logger
}

// NewMappingFile loads a mapping file from the given path.
func NewMappingFile(filePath string, logger *slog.Logger) (*MappingFile, error) {
	mf := &MappingFile{
		filePath: filePath,
		mappings: make(map[string]*AppInfo),
		logger:   logger,
	}

	if err := mf.Reload(); err != nil {
		return nil, err
	}

	return mf, nil
}

// Reload reads the mapping file from disk and updates the in-memory map.
func (mf *MappingFile) Reload() error {
	mf.mu.Lock()
	defer mf.mu.Unlock()

	data, err := os.ReadFile(mf.filePath)
	if err != nil {
		return fmt.Errorf("failed to read mapping file %s: %w", mf.filePath, err)
	}

	var fileData mappingFileData
	if err := json.Unmarshal(data, &fileData); err != nil {
		return fmt.Errorf("failed to parse mapping file: %w", err)
	}

	// Build a case-insensitive lookup map.
	newMappings := make(map[string]*AppInfo, len(fileData.Mappings))
	for exeName, info := range fileData.Mappings {
		newMappings[strings.ToLower(exeName)] = info
	}

	mf.mappings = newMappings
	mf.logger.Info("loaded software mappings", "count", len(newMappings), "version", fileData.Version)
	return nil
}

// Lookup searches for a mapping by exe name (case-insensitive).
func (mf *MappingFile) Lookup(exeName string) *AppInfo {
	mf.mu.RLock()
	defer mf.mu.RUnlock()
	return mf.mappings[strings.ToLower(exeName)]
}

// Count returns the number of loaded mappings.
func (mf *MappingFile) Count() int {
	mf.mu.RLock()
	defer mf.mu.RUnlock()
	return len(mf.mappings)
}
