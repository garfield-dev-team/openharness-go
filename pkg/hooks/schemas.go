// Package hooks provides hook definitions, execution, and lifecycle management.
package hooks

import (
	"encoding/json"
	"fmt"
)

// HookDefinition is the interface that all hook definition types implement.
type HookDefinition interface {
	// HookType returns the type discriminator (e.g. "command", "prompt", "http", "agent").
	HookType() string
	// GetMatcher returns the optional matcher pattern.
	GetMatcher() *string
	// GetBlockOnFailure returns whether execution should be blocked when the hook fails.
	GetBlockOnFailure() bool
	// GetTimeoutSeconds returns the timeout in seconds.
	GetTimeoutSeconds() int
}

// ---------------------------------------------------------------------------
// CommandHookDefinition
// ---------------------------------------------------------------------------

// CommandHookDefinition executes a shell command as a hook.
type CommandHookDefinition struct {
	Type           string  `json:"type"`            // always "command"
	Command        string  `json:"command"`
	TimeoutSeconds int     `json:"timeout_seconds"` // 1-600, default 30
	Matcher        *string `json:"matcher,omitempty"`
	BlockOnFailure bool    `json:"block_on_failure"`
}

func (d *CommandHookDefinition) HookType() string        { return "command" }
func (d *CommandHookDefinition) GetMatcher() *string      { return d.Matcher }
func (d *CommandHookDefinition) GetBlockOnFailure() bool  { return d.BlockOnFailure }
func (d *CommandHookDefinition) GetTimeoutSeconds() int   { return d.TimeoutSeconds }

// ---------------------------------------------------------------------------
// PromptHookDefinition
// ---------------------------------------------------------------------------

// PromptHookDefinition uses an LLM prompt to evaluate a condition.
type PromptHookDefinition struct {
	Type           string  `json:"type"`            // always "prompt"
	Prompt         string  `json:"prompt"`
	Model          *string `json:"model,omitempty"`
	TimeoutSeconds int     `json:"timeout_seconds"` // default 30
	Matcher        *string `json:"matcher,omitempty"`
	BlockOnFailure bool    `json:"block_on_failure"`
}

func (d *PromptHookDefinition) HookType() string        { return "prompt" }
func (d *PromptHookDefinition) GetMatcher() *string      { return d.Matcher }
func (d *PromptHookDefinition) GetBlockOnFailure() bool  { return d.BlockOnFailure }
func (d *PromptHookDefinition) GetTimeoutSeconds() int   { return d.TimeoutSeconds }

// ---------------------------------------------------------------------------
// HttpHookDefinition
// ---------------------------------------------------------------------------

// HttpHookDefinition sends an HTTP POST request as a hook.
type HttpHookDefinition struct {
	Type           string            `json:"type"`            // always "http"
	URL            string            `json:"url"`
	Headers        map[string]string `json:"headers"`
	TimeoutSeconds int               `json:"timeout_seconds"` // default 30
	Matcher        *string           `json:"matcher,omitempty"`
	BlockOnFailure bool              `json:"block_on_failure"`
}

func (d *HttpHookDefinition) HookType() string        { return "http" }
func (d *HttpHookDefinition) GetMatcher() *string      { return d.Matcher }
func (d *HttpHookDefinition) GetBlockOnFailure() bool  { return d.BlockOnFailure }
func (d *HttpHookDefinition) GetTimeoutSeconds() int   { return d.TimeoutSeconds }

// ---------------------------------------------------------------------------
// AgentHookDefinition
// ---------------------------------------------------------------------------

// AgentHookDefinition uses an LLM agent (with tool use) to evaluate a condition.
type AgentHookDefinition struct {
	Type           string  `json:"type"`            // always "agent"
	Prompt         string  `json:"prompt"`
	Model          *string `json:"model,omitempty"`
	TimeoutSeconds int     `json:"timeout_seconds"` // default 60
	Matcher        *string `json:"matcher,omitempty"`
	BlockOnFailure bool    `json:"block_on_failure"`
}

func (d *AgentHookDefinition) HookType() string        { return "agent" }
func (d *AgentHookDefinition) GetMatcher() *string      { return d.Matcher }
func (d *AgentHookDefinition) GetBlockOnFailure() bool  { return d.BlockOnFailure }
func (d *AgentHookDefinition) GetTimeoutSeconds() int   { return d.TimeoutSeconds }

// ---------------------------------------------------------------------------
// JSON unmarshalling helper
// ---------------------------------------------------------------------------

// typeProbe is used to peek at the "type" field during JSON unmarshalling.
type typeProbe struct {
	Type string `json:"type"`
}

// UnmarshalHookDefinition deserialises a JSON object into the correct
// concrete HookDefinition based on the "type" discriminator field.
func UnmarshalHookDefinition(data []byte) (HookDefinition, error) {
	var probe typeProbe
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("hooks: cannot determine hook type: %w", err)
	}

	switch probe.Type {
	case "command":
		var def CommandHookDefinition
		if err := json.Unmarshal(data, &def); err != nil {
			return nil, err
		}
		if def.TimeoutSeconds == 0 {
			def.TimeoutSeconds = 30
		}
		return &def, nil

	case "prompt":
		var def PromptHookDefinition
		if err := json.Unmarshal(data, &def); err != nil {
			return nil, err
		}
		if def.TimeoutSeconds == 0 {
			def.TimeoutSeconds = 30
		}
		// default block_on_failure = true for prompt hooks;
		// since Go zero-value is false, we must handle this via a raw message approach
		// but for simplicity we rely on explicit JSON. Users must set it explicitly
		// if they want false. We set the default in the loader when creating from config.
		return &def, nil

	case "http":
		var def HttpHookDefinition
		if err := json.Unmarshal(data, &def); err != nil {
			return nil, err
		}
		if def.TimeoutSeconds == 0 {
			def.TimeoutSeconds = 30
		}
		if def.Headers == nil {
			def.Headers = make(map[string]string)
		}
		return &def, nil

	case "agent":
		var def AgentHookDefinition
		if err := json.Unmarshal(data, &def); err != nil {
			return nil, err
		}
		if def.TimeoutSeconds == 0 {
			def.TimeoutSeconds = 60
		}
		return &def, nil

	default:
		return nil, fmt.Errorf("hooks: unknown hook type %q", probe.Type)
	}
}

// UnmarshalHookDefinitions deserialises a JSON array of hook definitions.
func UnmarshalHookDefinitions(data []byte) ([]HookDefinition, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("hooks: cannot unmarshal hook definitions array: %w", err)
	}
	defs := make([]HookDefinition, 0, len(raws))
	for i, raw := range raws {
		def, err := UnmarshalHookDefinition(raw)
		if err != nil {
			return nil, fmt.Errorf("hooks: definition[%d]: %w", i, err)
		}
		defs = append(defs, def)
	}
	return defs, nil
}
