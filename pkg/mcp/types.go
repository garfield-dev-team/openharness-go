// Package mcp provides types and a client for the Model Context Protocol.
package mcp

import (
	"encoding/json"
	"fmt"
)

// ---------------------------------------------------------------------------
// Server config types (union discriminated by "type")
// ---------------------------------------------------------------------------

// McpServerConfig is the interface satisfied by all MCP server configurations.
type McpServerConfig interface {
	// TransportType returns the transport discriminator ("stdio", "http", "ws").
	TransportType() string
}

// McpStdioServerConfig represents a server reached via a child process (stdin/stdout).
type McpStdioServerConfig struct {
	Type    string            `json:"type"`            // always "stdio"
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     *string           `json:"cwd,omitempty"`
}

func (c *McpStdioServerConfig) TransportType() string { return "stdio" }

// McpHttpServerConfig represents a server reached via HTTP.
type McpHttpServerConfig struct {
	Type    string            `json:"type"` // always "http"
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func (c *McpHttpServerConfig) TransportType() string { return "http" }

// McpWebSocketServerConfig represents a server reached via WebSocket.
type McpWebSocketServerConfig struct {
	Type    string            `json:"type"` // always "ws"
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func (c *McpWebSocketServerConfig) TransportType() string { return "ws" }

// UnmarshalServerConfig deserialises a JSON object into the correct
// concrete McpServerConfig based on the "type" discriminator field.
func UnmarshalServerConfig(data []byte) (McpServerConfig, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("mcp: cannot determine server config type: %w", err)
	}
	switch probe.Type {
	case "stdio":
		var cfg McpStdioServerConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
		if cfg.Args == nil {
			cfg.Args = []string{}
		}
		return &cfg, nil
	case "http":
		var cfg McpHttpServerConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
		if cfg.Headers == nil {
			cfg.Headers = make(map[string]string)
		}
		return &cfg, nil
	case "ws":
		var cfg McpWebSocketServerConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
		if cfg.Headers == nil {
			cfg.Headers = make(map[string]string)
		}
		return &cfg, nil
	default:
		return nil, fmt.Errorf("mcp: unknown server config type %q", probe.Type)
	}
}

// ---------------------------------------------------------------------------
// Tool / Resource info (immutable value objects)
// ---------------------------------------------------------------------------

// McpToolInfo describes a single tool exposed by an MCP server.
type McpToolInfo struct {
	ServerName  string         `json:"server_name"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// McpResourceInfo describes a single resource exposed by an MCP server.
type McpResourceInfo struct {
	ServerName  string `json:"server_name"`
	Name        string `json:"name"`
	URI         string `json:"uri"`
	Description string `json:"description,omitempty"`
}

// ---------------------------------------------------------------------------
// Connection status
// ---------------------------------------------------------------------------

// ConnectionState represents the state of a connection to an MCP server.
type ConnectionState string

const (
	StateConnected ConnectionState = "connected"
	StateFailed    ConnectionState = "failed"
	StatePending   ConnectionState = "pending"
	StateDisabled  ConnectionState = "disabled"
)

// McpConnectionStatus holds the runtime status of a single MCP server.
type McpConnectionStatus struct {
	Name           string          `json:"name"`
	State          ConnectionState `json:"state"`
	Detail         string          `json:"detail,omitempty"`
	Transport      string          `json:"transport"`
	AuthConfigured bool            `json:"auth_configured"`
	Tools          []McpToolInfo     `json:"tools"`
	Resources      []McpResourceInfo `json:"resources"`
}
