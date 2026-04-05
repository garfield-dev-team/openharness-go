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
// FileEditTool – edit a file by replacing old_string with new_string.
// ---------------------------------------------------------------------------

// FileEditInput is the expected JSON input for FileEditTool.
type FileEditInput struct {
	FilePath  string `json:"file_path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// FileEditTool edits a file by performing a search-and-replace.
type FileEditTool struct {
	tools.BaseToolHelper
}

// NewFileEditTool creates a FileEditTool instance.
func NewFileEditTool() *FileEditTool {
	return &FileEditTool{
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "Edit",
			ToolDescription: "Edit a file by replacing an exact occurrence of old_string with new_string.",
			ReadOnly:        false,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The path to the file to edit.",
					},
					"old_string": map[string]any{
						"type":        "string",
						"description": "The exact string to search for in the file.",
					},
					"new_string": map[string]any{
						"type":        "string",
						"description": "The string to replace old_string with.",
					},
				},
				"required": []string{"file_path", "old_string", "new_string"},
			},
		},
	}
}

// Execute performs the edit operation.
func (t *FileEditTool) Execute(_ context.Context, input json.RawMessage, execCtx *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in FileEditInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid FileEditTool input: %w", err)
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

	content := string(data)

	// Count occurrences to ensure uniqueness.
	count := strings.Count(content, in.OldString)
	if count == 0 {
		return tools.NewToolResultError("old_string not found in file"), nil
	}
	if count > 1 {
		return tools.NewToolResultError(fmt.Sprintf("old_string found %d times; it must be unique", count)), nil
	}

	newContent := strings.Replace(content, in.OldString, in.NewString, 1)

	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		return tools.NewToolResultError(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	return tools.NewToolResult(fmt.Sprintf("Successfully edited %s", in.FilePath)), nil
}
