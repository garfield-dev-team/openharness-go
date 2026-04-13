package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openharness/openharness/pkg/tasks"
	"github.com/openharness/openharness/pkg/tools"
)

type AgentTool struct {
	tools.BaseToolHelper
	executor *tasks.SubAgentExecutor
}

func NewAgentTool(executor *tasks.SubAgentExecutor) *AgentTool {
	return &AgentTool{
		executor: executor,
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "Agent",
			ToolDescription: "Spawn a sub-agent to work on a task. The sub-agent has its own conversation context and tool access. Use this for complex tasks that benefit from dedicated focus or parallel execution. The agent runs synchronously and returns its result.",
			ReadOnly:        false,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "The task description / objective for the sub-agent.",
					},
					"agent_type": map[string]any{
						"type":        "string",
						"description": "Type of sub-agent. general-purpose (full tool access), Explore (read-only), Plan (read + plan), Verification (read + test).",
						"enum":        []string{"general-purpose", "Explore", "Plan", "Verification"},
					},
				},
				"required": []string{"prompt"},
			},
		},
	}
}

func (t *AgentTool) Execute(ctx context.Context, input json.RawMessage, execCtx *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in struct {
		Prompt    string `json:"prompt"`
		AgentType string `json:"agent_type"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid Agent input: %w", err)
	}
	if in.Prompt == "" {
		return tools.NewToolResultError("prompt is required"), nil
	}
	if in.AgentType == "" {
		in.AgentType = string(tasks.SubAgentGeneral)
	}

	taskID, output, err := t.executor.SpawnAgentSync(ctx, tasks.SubAgentConfig{
		Type:   tasks.SubAgentType(in.AgentType),
		Prompt: in.Prompt,
	})
	if err != nil {
		return tools.NewToolResultError(fmt.Sprintf("Agent failed (task_id=%s): %v\n\nPartial output:\n%s", taskID, err, output)), nil
	}

	if output == "" {
		output = "(agent produced no output)"
	}

	return tools.NewToolResult(fmt.Sprintf("Agent completed (task_id=%s).\n\n%s", taskID, output)), nil
}
