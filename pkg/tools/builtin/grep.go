package builtin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/openharness/openharness/pkg/tools"
)

// ---------------------------------------------------------------------------
// GrepTool – search for a regex pattern across files.
// ---------------------------------------------------------------------------

// GrepInput is the expected JSON input for GrepTool.
type GrepInput struct {
	Pattern string  `json:"pattern"`
	Path    *string `json:"path,omitempty"`
	Include *string `json:"include,omitempty"` // filename glob filter
}

// GrepTool searches for a regex pattern in files.
type GrepTool struct {
	tools.BaseToolHelper
}

// NewGrepTool creates a GrepTool instance.
func NewGrepTool() *GrepTool {
	return &GrepTool{
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "Grep",
			ToolDescription: "Search for a regular expression pattern across files in a directory tree.",
			ReadOnly:        true,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Regular expression pattern to search for.",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Base directory to search in (defaults to cwd).",
					},
					"include": map[string]any{
						"type":        "string",
						"description": "Filename glob to filter which files to search (e.g. '*.go').",
					},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

// Execute searches for the pattern.
func (t *GrepTool) Execute(_ context.Context, input json.RawMessage, execCtx *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in GrepInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid GrepTool input: %w", err)
	}
	if in.Pattern == "" {
		return tools.NewToolResultError("pattern is required"), nil
	}

	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return tools.NewToolResultError(fmt.Sprintf("invalid regex: %v", err)), nil
	}

	baseDir := execCtx.Cwd
	if in.Path != nil && *in.Path != "" {
		p := *in.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(execCtx.Cwd, p)
		}
		baseDir = p
	}

	var results []string
	const maxResults = 500

	walkErr := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		// Apply include filter.
		if in.Include != nil && *in.Include != "" {
			matched, _ := filepath.Match(*in.Include, d.Name())
			if !matched {
				return nil
			}
		}
		if len(results) >= maxResults {
			return filepath.SkipAll
		}
		if err := grepFile(path, re, &results, maxResults); err != nil {
			// skip files we cannot read
			return nil
		}
		return nil
	})
	if walkErr != nil {
		return tools.NewToolResultError(fmt.Sprintf("grep walk error: %v", walkErr)), nil
	}

	if len(results) == 0 {
		return tools.NewToolResult("No matches found."), nil
	}

	return tools.NewToolResult(strings.Join(results, "\n")), nil
}

// grepFile searches a single file for matching lines.
func grepFile(path string, re *regexp.Regexp, results *[]string, max int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if re.MatchString(line) {
			*results = append(*results, fmt.Sprintf("%s:%d:%s", path, lineNo, line))
			if len(*results) >= max {
				return nil
			}
		}
	}
	return scanner.Err()
}
