// Package permissions defines permission modes for OpenHarness tool execution.
package permissions

// PermissionMode enumerates the supported permission modes.
type PermissionMode string

const (
	ModeDefault  PermissionMode = "default"
	ModePlan     PermissionMode = "plan"
	ModeFullAuto PermissionMode = "full_auto"
)

// String returns the string representation of the mode.
func (m PermissionMode) String() string { return string(m) }

// ValidPermissionModes contains all valid mode values.
var ValidPermissionModes = []PermissionMode{ModeDefault, ModePlan, ModeFullAuto}

// IsValid returns true if m is one of the known modes.
func (m PermissionMode) IsValid() bool {
	for _, v := range ValidPermissionModes {
		if m == v {
			return true
		}
	}
	return false
}
