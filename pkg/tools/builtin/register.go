package builtin

import (
	"github.com/openharness/openharness/pkg/tools"
)

// RegisterAll registers every built-in tool into the given registry.
func RegisterAll(reg *tools.ToolRegistry) {
	builtins := []tools.BaseTool{
		NewBashTool(),
		NewFileReadTool(),
		NewFileWriteTool(),
		NewFileEditTool(),
		NewGlobTool(),
		NewGrepTool(),
		NewAskUserQuestionTool(),
	}
	for _, t := range builtins {
		_ = reg.Register(t)
	}
}

// CreateDefaultToolRegistry is a convenience that creates a new ToolRegistry
// and registers all built-in tools.
func CreateDefaultToolRegistry() *tools.ToolRegistry {
	reg := tools.NewToolRegistry()
	RegisterAll(reg)
	return reg
}
