package normalizer

import (
	"log/slog"
	"strings"
	"sync"
)

// AppInfo is the normalized information about a software application.
type AppInfo struct {
	DisplayName string `json:"displayName"`
	Category    string `json:"category"`
	Publisher   string `json:"publisher"`
	Family      string `json:"family,omitempty"`
}

// Normalizer resolves raw exe names into friendly application names.
// It uses a tiered approach: PE metadata first, then mapping file.
type Normalizer struct {
	mapping *MappingFile
	pe      *PEReader
	cache   sync.Map // exePath -> *AppInfo
	logger  *slog.Logger
}

// NewNormalizer creates a new normalizer with the given mapping file and PE reader.
func NewNormalizer(mapping *MappingFile, pe *PEReader, logger *slog.Logger) *Normalizer {
	return &Normalizer{
		mapping: mapping,
		pe:      pe,
		logger:  logger,
	}
}

// Resolve returns the normalized AppInfo for a given executable.
// exeName is the process name (e.g., "EXCEL.EXE"), exePath is the full path.
func (n *Normalizer) Resolve(exeName, exePath string) *AppInfo {
	// Check cache first.
	if cached, ok := n.cache.Load(exePath); ok {
		return cached.(*AppInfo)
	}

	var info *AppInfo

	// Tier 1: Check mapping file (fast, authoritative).
	if n.mapping != nil {
		if mapped := n.mapping.Lookup(exeName); mapped != nil {
			info = mapped
		}
	}

	// Tier 2: Extract PE metadata from the executable.
	if info == nil && n.pe != nil && exePath != "" {
		if peInfo := n.pe.Extract(exePath); peInfo != nil {
			info = peInfo
		}
	}

	// Fallback: use the exe name itself, cleaned up.
	if info == nil {
		info = &AppInfo{
			DisplayName: cleanExeName(exeName),
			Category:    "Unknown",
			Publisher:   "Unknown",
		}
		n.logger.Debug("unresolved executable", "exe", exeName, "path", exePath)
	}

	// Cache the result.
	n.cache.Store(exePath, info)
	return info
}

// cleanExeName strips the .exe extension and title-cases the name.
func cleanExeName(name string) string {
	name = strings.TrimSuffix(name, ".exe")
	name = strings.TrimSuffix(name, ".EXE")
	if len(name) > 0 {
		return strings.ToUpper(name[:1]) + name[1:]
	}
	return name
}

// ResolveFamily returns the family key for a given executable, or empty string if none.
func (n *Normalizer) ResolveFamily(exeName, exePath string) string {
	info := n.Resolve(exeName, exePath)
	if info != nil {
		return info.Family
	}
	return ""
}

// ClearCache empties the resolution cache (e.g., after a mapping file update).
func (n *Normalizer) ClearCache() {
	n.cache = sync.Map{}
}
