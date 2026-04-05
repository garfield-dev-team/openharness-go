package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ---------------------------------------------------------------------------
// PermissionMode
// ---------------------------------------------------------------------------

// PermissionMode corresponds to Python PermissionMode enum.
type PermissionMode string

const (
	PermissionModeDefault  PermissionMode = "default"
	PermissionModePlan     PermissionMode = "plan"
	PermissionModeFullAuto PermissionMode = "full_auto"
)

// ---------------------------------------------------------------------------
// PathRuleConfig
// ---------------------------------------------------------------------------

// PathRuleConfig is a glob-pattern path permission rule.
type PathRuleConfig struct {
	Pattern string `json:"pattern"`
	Allow   bool   `json:"allow"`
}

// ---------------------------------------------------------------------------
// PermissionSettings
// ---------------------------------------------------------------------------

// PermissionSettings holds the permission mode configuration.
type PermissionSettings struct {
	Mode           PermissionMode   `json:"mode"`
	AllowedTools   []string         `json:"allowed_tools"`
	DeniedTools    []string         `json:"denied_tools"`
	PathRules      []PathRuleConfig `json:"path_rules"`
	DeniedCommands []string         `json:"denied_commands"`
}

// DefaultPermissionSettings returns a PermissionSettings with default values.
func DefaultPermissionSettings() PermissionSettings {
	return PermissionSettings{
		Mode:           PermissionModeDefault,
		AllowedTools:   []string{},
		DeniedTools:    []string{},
		PathRules:      []PathRuleConfig{},
		DeniedCommands: []string{},
	}
}

// ---------------------------------------------------------------------------
// MemorySettings
// ---------------------------------------------------------------------------

// MemorySettings corresponds to Python MemorySettings.
type MemorySettings struct {
	Enabled            bool `json:"enabled"`
	MaxFiles           int  `json:"max_files"`
	MaxEntrypointLines int  `json:"max_entrypoint_lines"`
}

// DefaultMemorySettings returns a MemorySettings with default values.
func DefaultMemorySettings() MemorySettings {
	return MemorySettings{
		Enabled:            true,
		MaxFiles:           5,
		MaxEntrypointLines: 200,
	}
}

// ---------------------------------------------------------------------------
// HookDefinition & McpServerConfig – lightweight placeholders used by Settings
// ---------------------------------------------------------------------------

// HookDefinitionConfig represents a single hook definition in the config file.
type HookDefinitionConfig struct {
	Command string `json:"command"`
}

// McpServerConfig represents the configuration for an MCP server.
type McpServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

// Settings is the main settings model for OpenHarness.
type Settings struct {
	// API configuration
	APIKey    string  `json:"api_key"`
	Model     string  `json:"model"`
	MaxTokens int     `json:"max_tokens"`
	BaseURL   *string `json:"base_url,omitempty"`

	// Behavior
	SystemPrompt *string            `json:"system_prompt,omitempty"`
	Permission   PermissionSettings `json:"permission"`

	// Hooks / plugins / MCP
	Hooks          map[string][]HookDefinitionConfig `json:"hooks"`
	Memory         MemorySettings                    `json:"memory"`
	EnabledPlugins map[string]bool                   `json:"enabled_plugins"`
	McpServers     map[string]McpServerConfig        `json:"mcp_servers"`

	// UI
	Theme       string `json:"theme"`
	OutputStyle string `json:"output_style"`
	VimMode     bool   `json:"vim_mode"`
	VoiceMode   bool   `json:"voice_mode"`
	FastMode    bool   `json:"fast_mode"`
	Effort      string `json:"effort"`
	Passes      int    `json:"passes"`
	Verbose     bool   `json:"verbose"`
}

// DefaultSettings returns a Settings with sensible defaults.
func DefaultSettings() Settings {
	return Settings{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 16384,
		Permission: DefaultPermissionSettings(),
		Hooks:          make(map[string][]HookDefinitionConfig),
		Memory:         DefaultMemorySettings(),
		EnabledPlugins: make(map[string]bool),
		McpServers:     make(map[string]McpServerConfig),
		Theme:          "default",
		OutputStyle:    "default",
		Effort:         "medium",
		Passes:         1,
	}
}

// ResolveAPIKey resolves the API key: instance value > env var > error.
func (s Settings) ResolveAPIKey() (string, error) {
	if s.APIKey != "" {
		return s.APIKey, nil
	}
	envKey := os.Getenv("ANTHROPIC_API_KEY")
	if envKey != "" {
		return envKey, nil
	}
	return "", fmt.Errorf("no API key found; set ANTHROPIC_API_KEY or configure api_key in %s", GetConfigFilePath())
}

// ---------------------------------------------------------------------------
// Load / Save
// ---------------------------------------------------------------------------

// LoadSettings reads the settings JSON file and returns a Settings.
func LoadSettings() (Settings, error) {
	path := GetConfigFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s := DefaultSettings()
			applyEnvOverrides(&s)
			return s, nil
		}
		return Settings{}, fmt.Errorf("reading settings file: %w", err)
	}
	s := DefaultSettings()
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, fmt.Errorf("parsing settings file: %w", err)
	}
	applyEnvOverrides(&s)
	return s, nil
}

// SaveSettings writes the settings to the config file as indented JSON.
func SaveSettings(s Settings) error {
	path := GetConfigFilePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling settings: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing settings file: %w", err)
	}
	return nil
}

func applyEnvOverrides(s *Settings) {
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		s.APIKey = v
	}
	if v := os.Getenv("ANTHROPIC_MODEL"); v != "" {
		s.Model = v
	} else if v := os.Getenv("OPENHARNESS_MODEL"); v != "" {
		s.Model = v
	}
	if v := os.Getenv("ANTHROPIC_BASE_URL"); v != "" {
		s.BaseURL = &v
	} else if v := os.Getenv("OPENHARNESS_BASE_URL"); v != "" {
		s.BaseURL = &v
	}
}
