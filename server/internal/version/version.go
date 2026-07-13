// Package version holds build metadata injected at compile time via -ldflags.
package version

var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)
