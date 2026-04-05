package hooks

// HookEvent represents a lifecycle event that can trigger hooks.
type HookEvent string

const (
	// PreToolUse is fired before a tool invocation.
	PreToolUse HookEvent = "pre_tool_use"
	// PostToolUse is fired after a tool invocation completes.
	PostToolUse HookEvent = "post_tool_use"
	// OnError is fired when an error occurs.
	OnError HookEvent = "on_error"
	// OnNotification is fired on notification events.
	OnNotification HookEvent = "on_notification"
)

// AllHookEvents returns all defined hook events.
func AllHookEvents() []HookEvent {
	return []HookEvent{PreToolUse, PostToolUse, OnError, OnNotification}
}

// IsValid reports whether e is a recognised hook event.
func (e HookEvent) IsValid() bool {
	switch e {
	case PreToolUse, PostToolUse, OnError, OnNotification:
		return true
	}
	return false
}

// String implements fmt.Stringer.
func (e HookEvent) String() string {
	return string(e)
}
