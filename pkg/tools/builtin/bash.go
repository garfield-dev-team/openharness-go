package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/openharness/openharness/pkg/tools"
)

// ---------------------------------------------------------------------------
// BashTool – execute a bash command with optional timeout.
// ---------------------------------------------------------------------------

// BashInput is the expected JSON input for BashTool.
type BashInput struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // seconds, default 120
}

// BashTool executes a bash command in a subprocess.
type BashTool struct {
	tools.BaseToolHelper
}

// NewBashTool creates a BashTool instance.
func NewBashTool() *BashTool {
	return &BashTool{
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "Bash",
			ToolDescription: "Execute a bash command in a subprocess with an optional timeout.",
			ReadOnly:        false,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The bash command to execute.",
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Timeout in seconds (default 120).",
						"default":     120,
					},
				},
				"required": []string{"command"},
			},
		},
	}
}

// Execute runs the bash command.
func (t *BashTool) Execute(ctx context.Context, input json.RawMessage, execCtx *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in BashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid BashTool input: %w", err)
	}
	if in.Command == "" {
		return tools.NewToolResultError("command is required"), nil
	}

	timeout := in.Timeout
	if timeout <= 0 {
		timeout = 120
	}

	cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", in.Command)
	cmd.Dir = execCtx.Cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return tools.NewToolResultError(fmt.Sprintf("command timed out after %d seconds\n%s", timeout, output)), nil
		}
		return tools.NewToolResultError(fmt.Sprintf("exit status error: %v\n%s", err, output)), nil
	}

	return tools.NewToolResult(output), nil
}
