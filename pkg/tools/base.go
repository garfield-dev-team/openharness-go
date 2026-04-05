// Package tools provides the base abstractions for tool execution.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// ToolExecutionContext carries the working directory and arbitrary metadata
// that a tool may need during execution.
type ToolExecutionContext struct {
	Cwd      string         `json:"cwd"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// NewToolExecutionContext creates a ToolExecutionContext with the given cwd.
func NewToolExecutionContext(cwd string) *ToolExecutionContext {
	return &ToolExecutionContext{Cwd: cwd, Metadata: make(map[string]any)}
}

// ToolResult is the immutable result of a single tool invocation.
type ToolResult struct {
	Output   string         `json:"output"`
	IsError  bool           `json:"is_error,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// NewToolResult creates a successful ToolResult.
func NewToolResult(output string) *ToolResult {
	return &ToolResult{Output: output, Metadata: make(map[string]any)}
}

// NewToolResultError creates a ToolResult representing an error.
func NewToolResultError(output string) *ToolResult {
	return &ToolResult{Output: output, IsError: true, Metadata: make(map[string]any)}
}

// BaseTool defines the contract every tool must satisfy.
type BaseTool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	Execute(ctx context.Context, input json.RawMessage, execCtx *ToolExecutionContext) (*ToolResult, error)
	IsReadOnly(input json.RawMessage) bool
	ToAPISchema() map[string]any
}

// BaseToolHelper provides default implementations for optional BaseTool methods.
type BaseToolHelper struct {
	ToolName        string
	ToolDescription string
	Schema          map[string]any
	ReadOnly        bool
}

func (h *BaseToolHelper) Name() string                        { return h.ToolName }
func (h *BaseToolHelper) Description() string                 { return h.ToolDescription }
func (h *BaseToolHelper) InputSchema() map[string]any         { return h.Schema }
func (h *BaseToolHelper) IsReadOnly(_ json.RawMessage) bool   { return h.ReadOnly }
func (h *BaseToolHelper) ToAPISchema() map[string]any {
	return map[string]any{
		"name":         h.ToolName,
		"description":  h.ToolDescription,
		"input_schema": h.Schema,
	}
}

// ToolRegistry is a thread-safe container for BaseTool instances.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]BaseTool
}

// NewToolRegistry creates an empty ToolRegistry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]BaseTool)}
}

// Register adds a tool to the registry.
func (r *ToolRegistry) Register(tool BaseTool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool already registered: %s", name)
	}
	r.tools[name] = tool
	return nil
}

// Get returns the tool with the given name, or nil if not found.
func (r *ToolRegistry) Get(name string) BaseTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// ListTools returns all registered tools.
func (r *ToolRegistry) ListTools() []BaseTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]BaseTool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// ToAPISchema returns the API schema for every registered tool.
func (r *ToolRegistry) ToAPISchema() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]map[string]any, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t.ToAPISchema())
	}
	return result
}
