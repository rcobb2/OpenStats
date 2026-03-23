package normalizer

// MetadataReader extracts AppInfo from a binary file path.
// On Windows this reads PE version resources; on macOS it reads Info.plist
// from the enclosing .app bundle.
type MetadataReader interface {
	Extract(exePath string) *AppInfo
}
