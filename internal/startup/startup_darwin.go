//go:build darwin

package startup

/*
#cgo LDFLAGS: -framework ServiceManagement

#include <stdlib.h>
#include "startup_darwin.h"
*/
import "C"
import "unsafe"

const (
	statusEnabled          = 1
	statusRequiresApproval = 2
)

// Enabled reports whether GateKeep is registered to open at login.
func Enabled() bool {
	switch C.startupStatus() {
	case statusEnabled, statusRequiresApproval:
		return true
	default:
		return false
	}
}

// NeedsApproval reports that macOS is waiting for the user to allow the login item.
func NeedsApproval() bool {
	return C.startupStatus() == statusRequiresApproval
}

// Set registers or removes GateKeep as a login item.
func Set(enabled bool) error {
	flag := C.int(0)
	if enabled {
		flag = 1
	}
	msg := C.startupSet(flag)
	if msg == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(msg))
	return errorString(C.GoString(msg))
}
