//go:build windows

package normalizer

import (
	"log/slog"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modVersion                  = windows.NewLazySystemDLL("version.dll")
	procGetFileVersionInfoW     = modVersion.NewProc("GetFileVersionInfoW")
	procGetFileVersionInfoSizeW = modVersion.NewProc("GetFileVersionInfoSizeW")
	procVerQueryValueW          = modVersion.NewProc("VerQueryValueW")
)

// PEReader extracts version info metadata from Windows PE executables.
type PEReader struct {
	logger *slog.Logger
}

// NewPEReader creates a new PE metadata reader.
func NewPEReader(logger *slog.Logger) *PEReader {
	return &PEReader{logger: logger}
}

// Extract reads the FileDescription and CompanyName from an executable's version resource.
func (p *PEReader) Extract(exePath string) (result *AppInfo) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Warn("recovered from panic reading PE metadata", "path", exePath, "panic", r)
			result = nil
		}
	}()

	// Only read from local fixed drives: UNC/WebDAV/device paths would make
	// the SYSTEM account authenticate to (or stall on) an attacker host.
	root, ok := isLocalDrivePath(exePath)
	if !ok {
		p.logger.Debug("skipping PE metadata for non-local path", "path", exePath)
		return nil
	}
	rootPtr, err := syscall.UTF16PtrFromString(root)
	if err != nil || windows.GetDriveType(rootPtr) != windows.DRIVE_FIXED {
		p.logger.Debug("skipping PE metadata for non-fixed drive", "path", exePath)
		return nil
	}

	pathPtr, err := syscall.UTF16PtrFromString(exePath)
	if err != nil {
		return nil
	}

	// Get the size of the version info.
	var handle uint32
	size, _, _ := procGetFileVersionInfoSizeW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&handle)),
	)
	const maxVersionInfoSize = 10 * 1024 * 1024 // sanity cap against malformed PE files
	if size == 0 || size > maxVersionInfoSize {
		return nil
	}

	// Allocate buffer and read version info.
	buf := make([]byte, size)
	ret, _, _ := procGetFileVersionInfoW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		size,
		uintptr(unsafe.Pointer(&buf[0])),
	)
	if ret == 0 {
		return nil
	}

	// Query for the translation table to find the correct code page.
	var transPtr *byte
	var transLen uint32
	subBlock, _ := syscall.UTF16PtrFromString(`\VarFileInfo\Translation`)
	ret, _, _ = procVerQueryValueW.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(subBlock)),
		uintptr(unsafe.Pointer(&transPtr)),
		uintptr(unsafe.Pointer(&transLen)),
	)

	// Default to English US / Unicode code page.
	langCodePage := "040904B0"
	if ret != 0 && transLen >= 4 {
		if b := bytesInBuf(buf, unsafe.Pointer(transPtr), uintptr(transLen)); b >= 4 {
			trans := unsafe.Slice((*uint16)(unsafe.Pointer(transPtr)), 2)
			langCodePage = sprintf04x(trans[0]) + sprintf04x(trans[1])
		}
	}

	description := queryStringValue(buf, langCodePage, "FileDescription")
	companyName := queryStringValue(buf, langCodePage, "CompanyName")

	if description == "" && companyName == "" {
		return nil
	}

	info := &AppInfo{
		DisplayName: description,
		Publisher:   companyName,
		Category:    inferCategoryFromPublisher(companyName, description),
	}

	p.logger.Debug("extracted PE metadata", "path", exePath, "name", description, "publisher", companyName)
	return info
}

// inferCategoryFromPublisher maps a Windows PE CompanyName to an app category.
// Called when the app isn't in the software-map.json mapping file.
func inferCategoryFromPublisher(publisher, displayName string) string {
	pub := strings.ToLower(publisher)
	desc := strings.ToLower(displayName)

	switch {
	case strings.Contains(pub, "adobe"):
		return "Creative"
	case strings.Contains(pub, "autodesk"):
		return "CAD/Engineering"
	case strings.Contains(pub, "mathworks"):
		return "Scientific Computing"
	case strings.Contains(pub, "wolfram"):
		return "Scientific Computing"
	case strings.Contains(pub, "ibm") && strings.Contains(desc, "spss"):
		return "Statistical Analysis"
	case strings.Contains(pub, "posit") || strings.Contains(pub, "rstudio"):
		return "Statistical Analysis"
	case strings.Contains(pub, "jetbrains"):
		return "Development"
	case strings.Contains(pub, "oracle") || strings.Contains(pub, "java"):
		return "Development"
	case strings.Contains(pub, "zoom video"):
		return "Communication"
	case strings.Contains(pub, "slack"):
		return "Communication"
	case strings.Contains(pub, "microsoft"):
		return inferMicrosoftCategory(desc)
	case strings.Contains(pub, "google llc") || strings.Contains(pub, "google inc"):
		return "Utility"
	case strings.Contains(pub, "mozilla"):
		return "Web Browser"
	case strings.Contains(pub, "python software"):
		return "Development"
	case strings.Contains(pub, "git"):
		return "Development"
	case strings.Contains(pub, "docker"):
		return "Development"
	case strings.Contains(pub, "atlassian"):
		return "Development"
	case strings.Contains(pub, "spotify"):
		return "Entertainment"
	case strings.Contains(pub, "dropbox"), strings.Contains(pub, "box, inc"):
		return "Productivity"
	case strings.Contains(pub, "vmware"), strings.Contains(pub, "broadcom"):
		return "Utility"
	}
	return "Unknown"
}

// inferMicrosoftCategory narrows Microsoft apps by display name.
func inferMicrosoftCategory(descLower string) string {
	switch {
	case strings.Contains(descLower, "edge"):
		return "Web Browser"
	case strings.Contains(descLower, "visual studio"), strings.Contains(descLower, "vscode"),
		strings.Contains(descLower, "powershell"), strings.Contains(descLower, "windows terminal"):
		return "Development"
	case strings.Contains(descLower, "teams"):
		return "Communication"
	case strings.Contains(descLower, "outlook"):
		return "Communication"
	case strings.Contains(descLower, "excel"), strings.Contains(descLower, "word"),
		strings.Contains(descLower, "powerpoint"), strings.Contains(descLower, "onenote"),
		strings.Contains(descLower, "access"), strings.Contains(descLower, "publisher"),
		strings.Contains(descLower, "onedrive"):
		return "Productivity"
	}
	return "Productivity"
}

func queryStringValue(buf []byte, langCodePage, key string) string {
	path := `\StringFileInfo\` + langCodePage + `\` + key
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}

	var valuePtr *uint16
	var valueLen uint32
	ret, _, _ := procVerQueryValueW.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&valuePtr)),
		uintptr(unsafe.Pointer(&valueLen)),
	)
	if ret == 0 || valueLen == 0 {
		return ""
	}

	// valueLen is in characters; the pointer must lie inside buf and the
	// value must fit in what remains (clamped, never read past the buffer).
	avail := bytesInBuf(buf, unsafe.Pointer(valuePtr), uintptr(valueLen)*2)
	n := int(avail / 2)
	if n <= 0 {
		return ""
	}
	return syscall.UTF16ToString(unsafe.Slice(valuePtr, n))
}

// bytesInBuf returns how many of the want bytes starting at ptr lie inside
// buf, or 0 if ptr is outside [&buf[0], &buf[len(buf)]).
func bytesInBuf(buf []byte, ptr unsafe.Pointer, want uintptr) uintptr {
	if len(buf) == 0 || ptr == nil {
		return 0
	}
	start := uintptr(unsafe.Pointer(&buf[0]))
	end := start + uintptr(len(buf))
	p := uintptr(ptr)
	if p < start || p >= end {
		return 0
	}
	if rem := end - p; want > rem {
		return rem
	}
	return want
}

// sprintf04x formats a uint16 as a zero-padded 4-digit hex string.
func sprintf04x(v uint16) string {
	const hex = "0123456789ABCDEF"
	return string([]byte{
		hex[(v>>12)&0xf],
		hex[(v>>8)&0xf],
		hex[(v>>4)&0xf],
		hex[v&0xf],
	})
}
