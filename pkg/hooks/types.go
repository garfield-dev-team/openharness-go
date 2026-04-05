package hooks

// HookResult captures the outcome of a single hook execution.
type HookResult struct {
	// HookType is the type discriminator of the hook that was executed (e.g. "command").
	HookType string `json:"hook_type"`
	// Success indicates whether the hook executed without errors.
	Success bool `json:"success"`
	// Output is the raw output produced by the hook (stdout, HTTP body, LLM response, etc.).
	Output string `json:"output,omitempty"`
	// Blocked indicates that the hook is requesting execution to be blocked.
	Blocked bool `json:"blocked"`
	// Reason provides a human-readable explanation when Blocked is true.
	Reason string `json:"reason,omitempty"`
	// Metadata holds arbitrary key/value data returned by the hook.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// AggregatedHookResult collects the results of all hooks executed for a single event.
type AggregatedHookResult struct {
	Results []HookResult `json:"results"`
}

// IsBlocked reports whether any hook in the aggregated result requested blocking.
func (a *AggregatedHookResult) IsBlocked() bool {
	for _, r := range a.Results {
		if r.Blocked {
			return true
		}
	}
	return false
}

// BlockReason returns the reason string from the first hook that requested blocking,
// or an empty string if no hook blocked.
func (a *AggregatedHookResult) BlockReason() string {
	for _, r := range a.Results {
		if r.Blocked {
			return r.Reason
		}
	}
	return ""
}
