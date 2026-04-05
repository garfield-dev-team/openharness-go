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
// FileReadTool – read a file, optionally with offset/limit.
// ---------------------------------------------------------------------------

// FileReadInput is the expected JSON input for FileReadTool.
type FileReadInput struct {
	FilePath string `json:"file_path"`
	Offset   *int   `json:"offset,omitempty"` // 0-based line offset
	Limit    *int   `json:"limit,omitempty"`  // max lines to return
}

// FileReadTool reads the content of a file.
type FileReadTool struct {
	tools.BaseToolHelper
}

// NewFileReadTool creates a FileReadTool instance.
func NewFileReadTool() *FileReadTool {
	return &FileReadTool{
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "Read",
			ToolDescription: "Read the contents of a file, optionally specifying a line offset and limit.",
			ReadOnly:        true,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The path to the file to read.",
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": "0-based line offset to start reading from.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of lines to return.",
					},
				},
				"required": []string{"file_path"},
			},
		},
	}
}

// Execute reads the file content.
func (t *FileReadTool) Execute(_ context.Context, input json.RawMessage, execCtx *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in FileReadInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid FileReadTool input: %w", err)
	}
	if in.FilePath == "" {
		return tools.NewToolResultError("file_path is required"), nil
	}

	path := in.FilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(execCtx.Cwd, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return tools.NewToolResultError(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	lines := strings.Split(string(data), "\n")

	offset := 0
	if in.Offset != nil && *in.Offset > 0 {
		offset = *in.Offset
	}
	if offset > len(lines) {
		offset = len(lines)
	}

	end := len(lines)
	if in.Limit != nil && *in.Limit > 0 {
		if offset+*in.Limit < end {
			end = offset + *in.Limit
		}
	}

	selected := lines[offset:end]
	return tools.NewToolResult(strings.Join(selected, "\n")), nil
}
