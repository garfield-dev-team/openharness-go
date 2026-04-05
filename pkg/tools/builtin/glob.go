package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openharness/openharness/pkg/tools"
)

// ---------------------------------------------------------------------------
// GlobTool – find files matching a glob pattern.
// ---------------------------------------------------------------------------

// GlobInput is the expected JSON input for GlobTool.
type GlobInput struct {
	Pattern string  `json:"pattern"`
	Path    *string `json:"path,omitempty"`
}

// GlobTool finds files that match a glob pattern.
type GlobTool struct {
	tools.BaseToolHelper
}

// NewGlobTool creates a GlobTool instance.
func NewGlobTool() *GlobTool {
	return &GlobTool{
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "Glob",
			ToolDescription: "Find files matching a glob pattern, optionally under a specific directory.",
			ReadOnly:        true,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Glob pattern to match files (e.g. '**/*.go').",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Base directory to search in (defaults to cwd).",
					},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

// Execute performs the glob search.
func (t *GlobTool) Execute(_ context.Context, input json.RawMessage, execCtx *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in GlobInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid GlobTool input: %w", err)
	}
	if in.Pattern == "" {
		return tools.NewToolResultError("pattern is required"), nil
	}

	baseDir := execCtx.Cwd
	if in.Path != nil && *in.Path != "" {
		p := *in.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(execCtx.Cwd, p)
		}
		baseDir = p
	}

	// Walk the directory tree and match the pattern.
	var matches []string
	err := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}
		rel, relErr := filepath.Rel(baseDir, path)
		if relErr != nil {
			return nil
		}
		matched, matchErr := filepath.Match(in.Pattern, rel)
		if matchErr != nil {
			return nil
		}
		// Also try matching against the base name for simple patterns.
		if !matched {
			matched, _ = filepath.Match(in.Pattern, d.Name())
		}
		if matched {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return tools.NewToolResultError(fmt.Sprintf("glob walk error: %v", err)), nil
	}

	if len(matches) == 0 {
		return tools.NewToolResult("No files matched the pattern."), nil
	}

	return tools.NewToolResult(strings.Join(matches, "\n")), nil
}
