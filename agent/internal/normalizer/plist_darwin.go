//go:build darwin

package normalizer

import (
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// PlistReader extracts AppInfo from macOS .app bundle Info.plist files.
// It implements the MetadataReader interface as the macOS equivalent of PEReader.
type PlistReader struct {
	logger *slog.Logger
}

// NewPlistReader creates a new macOS Info.plist metadata reader.
func NewPlistReader(logger *slog.Logger) *PlistReader {
	return &PlistReader{logger: logger}
}

// Extract reads app metadata from the Info.plist of the .app bundle containing
// exePath. It walks up the directory tree from exePath looking for a *.app
// directory, then reads Contents/Info.plist inside that bundle.
// Returns nil if no enclosing .app bundle is found or the plist can't be read
// (the normalizer then falls back to a cleaned version of the exe name).
func (p *PlistReader) Extract(exePath string) *AppInfo {
	bundlePath := findAppBundle(exePath)
	if bundlePath == "" {
		return nil
	}

	plistPath := filepath.Join(bundlePath, "Contents", "Info.plist")
	info, err := readPlistDict(plistPath)
	if err != nil {
		p.logger.Debug("plist read failed", "path", plistPath, "error", err)
		return nil
	}

	name := info["CFBundleDisplayName"]
	if name == "" {
		name = info["CFBundleName"]
	}
	if name == "" {
		return nil
	}

	return &AppInfo{
		DisplayName: name,
		Category:    "Unknown",
		Publisher:   "",
	}
}

// findAppBundle walks up from path until it finds a *.app directory.
// Stops searching at the filesystem root or /usr or /System to avoid runaway traversal.
func findAppBundle(path string) string {
	dir := filepath.Dir(path)
	for {
		if strings.HasSuffix(dir, ".app") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir || dir == "/" || dir == "/usr" || dir == "/System" {
			return ""
		}
		dir = parent
	}
}

// readPlistDict opens path and returns the top-level dict as a flat string map.
func readPlistDict(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var magic [6]byte
	n, _ := f.Read(magic[:])
	if n >= 6 && string(magic[:6]) == "bplist" {
		return nil, fmt.Errorf("binary plist not supported")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	return parsePlistDict(f)
}

// parsePlistDict parses the top-level <dict> of an Apple XML plist,
// returning string-valued keys only.
func parsePlistDict(r io.Reader) (map[string]string, error) {
	dec := xml.NewDecoder(r)
	result := make(map[string]string)

	for {
		tok, err := dec.Token()
		if err != nil {
			return result, nil
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "dict" {
			break
		}
	}

	var key string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "key":
				var s string
				if err := dec.DecodeElement(&s, &t); err == nil {
					key = s
				} else {
					key = ""
				}
			case "string":
				if key != "" {
					var s string
					if err := dec.DecodeElement(&s, &t); err == nil {
						result[key] = s
					}
					key = ""
				} else {
					dec.Skip() //nolint:errcheck
				}
			default:
				dec.Skip() //nolint:errcheck
				key = ""
			}
		case xml.EndElement:
			if t.Name.Local == "dict" {
				return result, nil
			}
		}
	}

	return result, nil
}
