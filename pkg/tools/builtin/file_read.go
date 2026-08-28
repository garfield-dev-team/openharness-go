package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openharness/openharness/pkg/hashline"
	"github.com/openharness/openharness/pkg/tools"
)

// ---------------------------------------------------------------------------
// FileReadTool – read a file with content anchors, optionally offset/limit.
// ---------------------------------------------------------------------------

// defaultReadLimit bounds a single Read so the model gets an actionable
// truncation hint instead of an oversized dump.
const defaultReadLimit = 250

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

const readToolDescription = `Read the contents of a file. Every line is prefixed with a 3-character ` +
	`content anchor (e.g. "aB9|func main() {"). To edit lines, pass these anchors to the Edit tool — ` +
	`do not re-type whole lines as search text. Defaults to ` + "250" + ` lines; ` +
	`if output is truncated, call again with "offset" to continue.`

// NewFileReadTool creates a FileReadTool instance.
func NewFileReadTool() *FileReadTool {
	return &FileReadTool{
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "Read",
			ToolDescription: readToolDescription,
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
						"description": "Maximum number of lines to return (default 250).",
					},
				},
				"required": []string{"file_path"},
			},
		},
	}
}

// Execute reads the file content and renders it in anchor format.
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
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1] // trailing newline is not a line
	}

	offset := 0
	if in.Offset != nil && *in.Offset > 0 {
		offset = *in.Offset
	}
	if offset > len(lines) {
		offset = len(lines)
	}

	limit := defaultReadLimit
	if in.Limit != nil && *in.Limit > 0 {
		limit = *in.Limit
	}
	end := min(offset+limit, len(lines))

	out := hashline.Encode(lines[offset:end])
	if end < len(lines) {
		out += fmt.Sprintf("... (%d more lines; call Read again with offset=%d to continue)\n", len(lines)-end, end)
	}
	return tools.NewToolResult(out), nil
}
