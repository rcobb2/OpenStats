//go:build !windows && !darwin

package session

import "time"

// ForegroundPID is unsupported on this platform.
func ForegroundPID() uint32 { return 0 }

// IdleTime is unsupported on this platform.
func IdleTime() time.Duration { return 0 }
