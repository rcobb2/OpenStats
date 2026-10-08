//go:build darwin

package inventory

import (
	"bytes"
	"encoding/xml"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Scan reads installed applications from /Applications by walking .app bundles
// and reading their Info.plist files.
func (s *Scanner) Scan() []InstalledApp {
	seen := make(map[string]bool) // deduplicate by bundle identifier
	var result []InstalledApp

	err := filepath.WalkDir("/Applications", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if !d.IsDir() || !strings.HasSuffix(d.Name(), ".app") {
			return nil
		}

		// Limit depth: allow /Applications/*.app and /Applications/Utilities/*.app
		// but skip deeply nested .app bundles.
		rel, _ := filepath.Rel("/Applications", path)
		if strings.Count(rel, string(os.PathSeparator)) > 1 {
			return filepath.SkipDir
		}

		plistPath := filepath.Join(path, "Contents", "Info.plist")
		info, err := readInfoPlist(plistPath)
		if err != nil {
			return filepath.SkipDir // no readable plist, skip bundle
		}

		bundleID := info["CFBundleIdentifier"]
		if bundleID == "" {
			bundleID = path
		}
		if seen[bundleID] {
			return filepath.SkipDir
		}
		seen[bundleID] = true

		name := info["CFBundleDisplayName"]
		if name == "" {
			name = info["CFBundleName"]
		}
		if name == "" {
			name = strings.TrimSuffix(d.Name(), ".app")
		}

		result = append(result, InstalledApp{
			Name:      name,
			Version:   info["CFBundleShortVersionString"],
			Publisher: "",
		})

		return filepath.SkipDir // don't recurse inside .app bundle
	})
	if err != nil {
		s.logger.Warn("inventory walk error", "error", err)
	}

	s.logger.Info("macOS inventory scan complete", "count", len(result))
	return result
}

// readInfoPlist opens path and parses it as an Apple property list (XML or
// binary), returning the top-level dictionary as a flat string map.
//
// Binary-encoded Info.plist (magic "bplist") isn't a rare edge case — it's
// Xcode's standard output for most Release/Archive builds, so treating it as
// unsupported silently dropped a significant, unpredictable fraction of real
// installed apps from inventory (the caller skips the whole bundle on any
// read error, with no per-bundle warning — only an aggregate scan count).
// There's no plist-binary support in the standard library, but every real
// macOS machine ships /usr/bin/plutil, which converts in place; shelling out
// to it is simpler and more reliable than reimplementing Apple's bplist00
// binary format from scratch.
func readInfoPlist(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Detect binary plist by checking the magic bytes.
	var magic [6]byte
	n, _ := f.Read(magic[:])
	isBinary := n >= 6 && string(magic[:6]) == "bplist"
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	if !isBinary {
		return parseXMLPlistDict(f)
	}

	out, err := exec.Command("plutil", "-convert", "xml1", "-o", "-", path).Output()
	if err != nil {
		return nil, err
	}
	return parseXMLPlistDict(bytes.NewReader(out))
}

// parseXMLPlistDict parses the top-level <dict> of an Apple XML plist,
// returning string-valued keys only. Nested structures are skipped.
func parseXMLPlistDict(r io.Reader) (map[string]string, error) {
	dec := xml.NewDecoder(r)
	result := make(map[string]string)

	// Advance to the first <dict> element.
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
				// Non-string value (bool, integer, array, dict, etc.): skip.
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
