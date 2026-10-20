//go:build windows

package monitor

import (
	"errors"

	"github.com/go-ole/go-ole"
)

const (
	wbemErrTimedout = 0x80043001 // WBEM_E_TIMED_OUT: NextEvent found no event in the window
	dispEException  = 0x80020009 // DISP_E_EXCEPTION: real HRESULT is in EXCEPINFO.scode
)

// isWMITimeout reports whether err from SWbemEventSource.NextEvent is the
// benign "no event within the timeout" result. go-ole surfaces it either as the
// OleError code itself or, for IDispatch.Invoke, as DISP_E_EXCEPTION with the
// WBEM code in the EXCEPINFO scode (carried as the OleError's SubError).
// Anything else (RPC unavailable, subscription invalidated, WMI restarted...)
// is NOT a timeout and must not be retried in a tight loop.
func isWMITimeout(err error) bool {
	var oleErr *ole.OleError
	if !errors.As(err, &oleErr) || oleErr == nil {
		return false
	}
	if uint32(oleErr.Code()) == wbemErrTimedout {
		return true
	}
	if uint32(oleErr.Code()) != dispEException {
		return false
	}
	switch ex := oleErr.SubError().(type) {
	case ole.EXCEPINFO:
		return ex.SCODE() == wbemErrTimedout
	case *ole.EXCEPINFO:
		return ex != nil && ex.SCODE() == wbemErrTimedout
	}
	return false
}
