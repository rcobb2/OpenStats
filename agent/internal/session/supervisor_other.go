//go:build !windows && !darwin

package session

import (
	"errors"
	"log/slog"
)

func newPlatform(*slog.Logger) (platform, error) {
	return nil, errors.New("session helpers are not supported on this platform")
}
