//go:build darwin

package normalizer

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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

	category, publisher := inferFromBundleID(info["CFBundleIdentifier"])

	return &AppInfo{
		DisplayName: name,
		Category:    category,
		Publisher:   publisher,
	}
}

// inferFromBundleID maps a CFBundleIdentifier to a category and publisher.
// Returns ("Unknown", "") when no pattern matches.
func inferFromBundleID(id string) (category, publisher string) {
	switch {
	case strings.HasPrefix(id, "com.adobe."):
		return "Creative", "Adobe Inc."
	case id == "com.microsoft.Word", id == "com.microsoft.Excel",
		id == "com.microsoft.Powerpoint", id == "com.microsoft.onenote",
		id == "com.microsoft.OneDrive":
		return "Productivity", "Microsoft Corporation"
	case id == "com.microsoft.teams", id == "com.microsoft.teams2",
		id == "com.microsoft.Outlook":
		return "Communication", "Microsoft Corporation"
	case id == "com.microsoft.edgemac":
		return "Web Browser", "Microsoft Corporation"
	case id == "com.microsoft.VSCode":
		return "Development", "Microsoft Corporation"
	case strings.HasPrefix(id, "com.microsoft."):
		return "Productivity", "Microsoft Corporation"
	case id == "com.apple.Safari":
		return "Web Browser", "Apple Inc."
	case id == "com.apple.dt.Xcode", id == "com.apple.Xcode":
		return "Development", "Apple Inc."
	case id == "com.apple.FinalCut", id == "com.apple.logic10",
		id == "com.apple.garageband10", id == "com.apple.motionapp5",
		id == "com.apple.Compressor":
		return "Creative", "Apple Inc."
	case id == "com.apple.iWork.Pages", id == "com.apple.iWork.Numbers",
		id == "com.apple.iWork.Keynote":
		return "Productivity", "Apple Inc."
	case strings.HasPrefix(id, "com.apple.dt."):
		return "Development", "Apple Inc."
	case strings.HasPrefix(id, "com.apple."):
		return "System", "Apple Inc."
	case id == "com.google.Chrome":
		return "Web Browser", "Google LLC"
	case id == "com.google.GoogleDrive":
		return "Productivity", "Google LLC"
	case strings.HasPrefix(id, "com.google."):
		return "Utility", "Google LLC"
	case id == "org.mozilla.firefox":
		return "Web Browser", "Mozilla Foundation"
	case id == "com.zoom.xos", id == "us.zoom.xos":
		return "Communication", "Zoom Video Communications"
	case strings.HasPrefix(id, "com.slack-technologies."), strings.HasPrefix(id, "com.tinyspeck.slackmacgap"):
		return "Communication", "Slack Technologies"
	case id == "com.github.GitHub":
		return "Development", "GitHub Inc."
	case strings.HasPrefix(id, "com.autodesk."):
		return "CAD/Engineering", "Autodesk Inc."
	case strings.HasPrefix(id, "com.mathworks."):
		return "Scientific Computing", "MathWorks Inc."
	case strings.HasPrefix(id, "com.ibm.spss."), strings.HasPrefix(id, "com.spss."):
		return "Statistical Analysis", "IBM Corp."
	case id == "org.rstudio.RStudio":
		return "Statistical Analysis", "Posit PBC"
	case strings.HasPrefix(id, "com.wolfram."):
		return "Scientific Computing", "Wolfram Research"
	case id == "com.spotify.client":
		return "Entertainment", "Spotify AB"
	case strings.HasPrefix(id, "com.jamfsoftware."), strings.HasPrefix(id, "com.jamf."):
		return "System", "Jamf"
	case strings.HasPrefix(id, "com.panopto."):
		return "Utility", "Panopto"
	case id == "com.apple.dt.instruments", id == "com.apple.dt.InstrumentsClient":
		return "Development", "Apple Inc."
	case id == "net.sourceforge.skim-app.skim":
		return "Document Viewer", "Skim Project"
	case id == "com.readdle.PDFExpert-Mac":
		return "Document Viewer", "Readdle Inc."
	case id == "com.microsoft.rdc.macos", id == "com.microsoft.rdc.mac":
		return "Utility", "Microsoft Corporation"
	case id == "org.videolan.vlc":
		return "Entertainment", "VideoLAN"
	case id == "org.gimp.gimp":
		return "Creative", "GIMP Project"
	case id == "com.sublimetext.4", id == "com.sublimetext.3":
		return "Development", "Sublime HQ"
	case id == "com.jetbrains.intellij", strings.HasPrefix(id, "com.jetbrains."):
		return "Development", "JetBrains"
	case id == "com.github.atom":
		return "Development", "GitHub Inc."
	case id == "io.brackets.CEF":
		return "Development", "Adobe Inc."
	case id == "com.dropbox.DropboxMacUpdate", strings.HasPrefix(id, "com.getdropbox."),
		strings.HasPrefix(id, "com.dropbox."):
		return "Productivity", "Dropbox Inc."
	case id == "com.box.desktop", strings.HasPrefix(id, "com.box."):
		return "Productivity", "Box Inc."
	case strings.HasPrefix(id, "com.vmware."):
		return "Utility", "VMware Inc."
	case id == "org.7-zip.7-zip":
		return "Utility", "7-Zip Project"
	case id == "fm.receiver.Receiver":
		return "Utility", "Receiver"
	case strings.HasPrefix(id, "com.cisco."):
		return "Communication", "Cisco Systems"
	case id == "com.webex.meetingmanager", id == "Cisco-Systems.Spark",
		strings.HasPrefix(id, "com.webex."):
		return "Communication", "Cisco Systems"
	case strings.HasPrefix(id, "com.atlassian."):
		return "Development", "Atlassian"
	}
	return "Unknown", ""
}

// findAppBundle walks up from path until it finds a *.app directory.
// Stops at filesystem root, /usr, /System, or after 10 levels to bound traversal cost.
func findAppBundle(path string) string {
	dir := filepath.Dir(path)
	for depth := 0; depth < 10; depth++ {
		if strings.HasSuffix(dir, ".app") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir || dir == "/" || dir == "/usr" || dir == "/System" || dir == "/Library" {
			return ""
		}
		dir = parent
	}
	return ""
}

// readPlistDict opens path and returns the top-level dict as a flat string map.
func readPlistDict(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	var magic [6]byte
	n, _ := f.Read(magic[:])
	f.Close()

	if n >= 6 && string(magic[:6]) == "bplist" {
		// Convert binary plist to XML using plutil (ships with macOS).
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "plutil", "-convert", "xml1", "-o", "-", path).Output()
		if err != nil {
			return nil, fmt.Errorf("binary plist conversion failed: %w", err)
		}
		return parsePlistDict(bytes.NewReader(out))
	}

	// Re-open for XML parsing since we consumed the magic bytes.
	f2, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f2.Close()
	return parsePlistDict(f2)
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
