//go:build windows

package normalizer

import (
	"log/slog"
	"strings"
	"syscall"
	"unsafe"
)

var (
	modVersion              = syscall.NewLazyDLL("version.dll")
	procGetFileVersionInfoW = modVersion.NewProc("GetFileVersionInfoW")
	procGetFileVersionInfoSizeW = modVersion.NewProc("GetFileVersionInfoSizeW")
	procVerQueryValueW      = modVersion.NewProc("VerQueryValueW")
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
func (p *PEReader) Extract(exePath string) *AppInfo {
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
	if size == 0 {
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
		trans := (*[2]uint16)(unsafe.Pointer(transPtr))
		langCodePage = sprintf04x(trans[0]) + sprintf04x(trans[1])
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

	// Convert the UTF-16 string to Go string.
	return syscall.UTF16ToString((*[1 << 15]uint16)(unsafe.Pointer(valuePtr))[:valueLen:valueLen])
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
