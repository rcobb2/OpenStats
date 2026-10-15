//go:build !darwin

package session

// DropToUID is only needed on macOS, where the helper is launched as root.
func DropToUID(uid int) error { return nil }
