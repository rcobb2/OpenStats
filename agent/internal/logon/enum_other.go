//go:build !windows && !darwin

package logon

// unsupportedEnumerator stands in on platforms the agent does not ship to. It
// exists so the package — and the tracker logic and its tests — build and run
// anywhere, including CI and developer machines that are neither Windows nor
// macOS.
type unsupportedEnumerator struct{}

// NewEnumerator returns an enumerator that reports no sessions.
func NewEnumerator() Enumerator { return &unsupportedEnumerator{} }

func (e *unsupportedEnumerator) Enumerate() ([]Session, error) { return nil, nil }
