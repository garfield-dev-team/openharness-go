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
// FileEditTool – edit a file by replacing anchored lines.
// ---------------------------------------------------------------------------

// AnchorEdit replaces one anchored line with new content. Disambiguators are
// only needed when the anchor matches multiple lines in the current file.
type AnchorEdit struct {
	Anchor     string `json:"anchor"`               // 3-char hash from Read output
	LineHint   int    `json:"line,omitempty"`       // 1-based line number, for disambiguation
	Occurrence int    `json:"occurrence,omitempty"` // 1-based which-match, for disambiguation
	NewLines   string `json:"new_lines"`            // full replacement content (may be multi-line)
}

// FileEditInput is the expected JSON input for FileEditTool. All anchors are
// validated against the current file before anything is written: the edit is
// all-or-nothing.
type FileEditInput struct {
	FilePath string       `json:"file_path"`
	Anchors  []AnchorEdit `json:"anchors"`
}

// FileEditTool edits a file by swapping out anchored lines.
type FileEditTool struct {
	tools.BaseToolHelper
}

const editToolDescription = `Edit a file by replacing lines identified by their content anchors from the ` +
	`Read tool (each Read output line starts with a 3-character anchor like "aB9|"). Pass one entry per ` +
	`edited line in "anchors" with the full replacement text in "new_lines" — never re-type unchanged ` +
	`lines as search text. If an anchor matches several lines, add "occurrence" (1-based which match) or ` +
	`"line" (1-based line number). Multiple anchors are applied atomically: if any anchor is stale or ` +
	`ambiguous nothing is written.`

// NewFileEditTool creates a FileEditTool instance.
func NewFileEditTool() *FileEditTool {
	return &FileEditTool{
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "Edit",
			ToolDescription: editToolDescription,
			ReadOnly:        false,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The path to the file to edit.",
					},
					"anchors": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"anchor": map[string]any{
									"type":        "string",
									"description": "The 3-character content anchor from Read output.",
								},
								"line": map[string]any{
									"type":        "integer",
									"description": "Optional 1-based line number when the anchor matches multiple lines.",
								},
								"occurrence": map[string]any{
									"type":        "integer",
									"description": "Optional 1-based index of which matching line to edit.",
								},
								"new_lines": map[string]any{
									"type":        "string",
									"description": "Full replacement content; may span multiple lines.",
								},
							},
							"required": []string{"anchor", "new_lines"},
						},
					},
				},
				"required": []string{"file_path", "anchors"},
			},
		},
	}
}

// Execute validates every anchor against the current file, then applies all
// replacements and writes atomically via temp-file + rename.
func (t *FileEditTool) Execute(_ context.Context, input json.RawMessage, execCtx *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in FileEditInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid FileEditTool input: %w", err)
	}
	if in.FilePath == "" {
		return tools.NewToolResultError("file_path is required"), nil
	}
	if len(in.Anchors) == 0 {
		return tools.NewToolResultError("anchors must contain at least one entry"), nil
	}

	path := in.FilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(execCtx.Cwd, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return tools.NewToolResultError(fmt.Sprintf("failed to read file: %v — call Read on it first to obtain fresh anchors", err)), nil
	}

	lines := splitLines(data)

	reqs := make([]hashline.Request, len(in.Anchors))
	for i, a := range in.Anchors {
		if len(a.Anchor) != hashline.AnchorLen {
			return tools.NewToolResultError(fmt.Sprintf("anchor %q must be exactly %d characters — copy it verbatim from the Read output prefix", a.Anchor, hashline.AnchorLen)), nil
		}
		reqs[i] = hashline.Request{Anchor: a.Anchor, LineHint: a.LineHint, Occurrence: a.Occurrence}
	}

	res, err := hashline.Resolve(hashline.Index(lines), reqs)
	if err != nil {
		return tools.NewToolResultError(err.Error()), nil
	}

	newLines := hashline.Apply(lines, res, func(r hashline.Resolution) string {
		for _, a := range in.Anchors {
			if a.Anchor == r.Anchor {
				return a.NewLines
			}
		}
		return ""
	})

	if err := writeFileAtomic(path, []byte(strings.Join(newLines, "\n")+"\n")); err != nil {
		return tools.NewToolResultError(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	return tools.NewToolResult(fmt.Sprintf("Successfully edited %d line(s) in %s; re-Read before further edits to refresh anchors", len(res), in.FilePath)), nil
}

func splitLines(data []byte) []string {
	lines := strings.Split(string(data), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".oh-edit-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	info, err := os.Stat(path)
	if err == nil {
		if err := os.Chmod(tmpName, info.Mode().Perm()); err != nil {
			return fmt.Errorf("preserve mode: %w", err)
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}
