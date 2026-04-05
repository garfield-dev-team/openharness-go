package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openharness/openharness/pkg/tools"
)

// ---------------------------------------------------------------------------
// FileWriteTool – write content to a file (create or overwrite).
// ---------------------------------------------------------------------------

// FileWriteInput is the expected JSON input for FileWriteTool.
type FileWriteInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// FileWriteTool writes content to a file.
type FileWriteTool struct {
	tools.BaseToolHelper
}

// NewFileWriteTool creates a FileWriteTool instance.
func NewFileWriteTool() *FileWriteTool {
	return &FileWriteTool{
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "Write",
			ToolDescription: "Write content to a file, creating it if necessary or overwriting existing content.",
			ReadOnly:        false,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The path to the file to write.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The content to write to the file.",
					},
				},
				"required": []string{"file_path", "content"},
			},
		},
	}
}

// Execute writes the content to the file.
func (t *FileWriteTool) Execute(_ context.Context, input json.RawMessage, execCtx *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in FileWriteInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid FileWriteTool input: %w", err)
	}
	if in.FilePath == "" {
		return tools.NewToolResultError("file_path is required"), nil
	}

	path := in.FilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(execCtx.Cwd, path)
	}

	// Ensure parent directories exist.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return tools.NewToolResultError(fmt.Sprintf("failed to create directory %s: %v", dir, err)), nil
	}

	if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
		return tools.NewToolResultError(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	return tools.NewToolResult(fmt.Sprintf("Successfully wrote %d bytes to %s", len(in.Content), in.FilePath)), nil
}
