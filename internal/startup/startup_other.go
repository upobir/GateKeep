//go:build !darwin

package startup

// Enabled reports whether GateKeep is registered to open at login.
func Enabled() bool {
	return false
}

// NeedsApproval reports that macOS is waiting for the user to allow the login item.
func NeedsApproval() bool {
	return false
}

// Set registers or removes GateKeep as a login item.
func Set(bool) error {
	return errorString("startup at login is only available on macOS")
}
