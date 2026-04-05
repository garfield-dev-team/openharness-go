package hooks

import (
	"encoding/json"
	"fmt"
	"sync"
)

// HookRegistry stores hook definitions grouped by event.
type HookRegistry struct {
	mu    sync.RWMutex
	hooks map[HookEvent][]HookDefinition
}

// NewHookRegistry creates an empty registry.
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{
		hooks: make(map[HookEvent][]HookDefinition),
	}
}

// Register adds a hook definition for the given event.
func (r *HookRegistry) Register(event HookEvent, def HookDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks[event] = append(r.hooks[event], def)
}

// Get returns all hook definitions registered for the given event.
// The returned slice must not be modified by the caller.
func (r *HookRegistry) Get(event HookEvent) []HookDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hooks[event]
}

// ---------------------------------------------------------------------------
// HooksConfig is the top-level configuration structure expected when loading
// hooks from a settings file / JSON.
// ---------------------------------------------------------------------------

// HooksConfig maps event names to lists of raw hook definitions.
type HooksConfig map[string][]json.RawMessage

// FromSettings constructs a HookRegistry from a HooksConfig. Each key in the
// config should be a valid HookEvent string, and each value is an array of
// hook definition JSON objects.
func FromSettings(cfg HooksConfig) (*HookRegistry, error) {
	reg := NewHookRegistry()
	for eventStr, rawDefs := range cfg {
		event := HookEvent(eventStr)
		if !event.IsValid() {
			return nil, fmt.Errorf("hooks: unknown event %q", eventStr)
		}
		for i, raw := range rawDefs {
			def, err := UnmarshalHookDefinition(raw)
			if err != nil {
				return nil, fmt.Errorf("hooks: event %q definition[%d]: %w", eventStr, i, err)
			}
			applyDefaults(def)
			reg.Register(event, def)
		}
	}
	return reg, nil
}

// applyDefaults sets Python-compatible defaults that cannot be expressed with
// Go zero values (e.g. block_on_failure defaults to true for prompt/agent).
func applyDefaults(def HookDefinition) {
	// block_on_failure defaults are handled during UnmarshalHookDefinition
	// for types where Go's zero-value (false) differs from Python's default (True).
	// Because JSON bool false and absent are indistinguishable after Unmarshal,
	// we use a convention: if the user wants block_on_failure=false for prompt/agent
	// hooks they must set it explicitly. Here we apply the "true" default for types
	// where that is the Python behaviour, but only if the raw JSON did not contain
	// the field. Since we lose that information after Unmarshal, prompt and agent
	// hooks default block_on_failure to true in their Unmarshal path above.
	//
	// For now this function is a placeholder for additional default logic.
	switch d := def.(type) {
	case *PromptHookDefinition:
		// Python default: block_on_failure = True
		// We cannot distinguish "explicitly set to false" from "not set" here
		// without using *bool. For safety, we keep whatever was deserialised.
		_ = d
	case *AgentHookDefinition:
		_ = d
	}
}
