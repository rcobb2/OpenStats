//go:build windows

package monitor

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-ole/go-ole"
)

func TestIsWMITimeout(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("boom"), false},
		{"direct timeout", ole.NewError(0x80043001), true},
		{"wrapped timeout", fmt.Errorf("x: %w", ole.NewError(0x80043001)), true},
		{"rpc unavailable", ole.NewError(0x800706BA), false},
		{"invalidated sub", ole.NewError(0x80041032), false},
		// scode is unexported in go-ole, so a zero EXCEPINFO (scode 0) can only
		// exercise the "exception but not a timeout" path.
		{"exception non-timeout", ole.NewErrorWithSubError(0x80020009, "", ole.EXCEPINFO{}), false},
		{"exception ptr non-timeout", ole.NewErrorWithSubError(0x80020009, "", &ole.EXCEPINFO{}), false},
		{"exception no sub", ole.NewError(0x80020009), false},
	}
	for _, c := range cases {
		if got := isWMITimeout(c.err); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
