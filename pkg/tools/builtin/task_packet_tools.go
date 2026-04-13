package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openharness/openharness/pkg/tasks"
	"github.com/openharness/openharness/pkg/tools"
)

type TaskPacketCreateTool struct {
	tools.BaseToolHelper
}

func NewTaskPacketCreateTool() *TaskPacketCreateTool {
	return &TaskPacketCreateTool{
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "TaskPacketCreate",
			ToolDescription: "Create a structured TaskPacket that defines a task contract with objective, scope, acceptance tests, and policies.",
			ReadOnly:        false,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"objective": map[string]any{
						"type":        "string",
						"description": "The main objective of the task.",
					},
					"scope": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "File paths or directories in scope for this task.",
					},
					"acceptance_tests": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Commands to run to verify the task is complete (e.g. 'go test ./...').",
					},
					"branch_policy": map[string]any{
						"type":        "string",
						"description": "Branch policy: auto_create, current, or specific:<name>.",
					},
					"commit_policy": map[string]any{
						"type":        "string",
						"description": "Commit policy: auto_commit, stage_only, or none.",
					},
				},
				"required": []string{"objective"},
			},
		},
	}
}

func (t *TaskPacketCreateTool) Execute(_ context.Context, input json.RawMessage, _ *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var packet tasks.TaskPacket
	if err := json.Unmarshal(input, &packet); err != nil {
		return nil, fmt.Errorf("invalid TaskPacketCreate input: %w", err)
	}
	if packet.Objective == "" {
		return tools.NewToolResultError("objective is required"), nil
	}
	data, _ := json.MarshalIndent(packet, "", "  ")
	return tools.NewToolResult(fmt.Sprintf("TaskPacket created:\n%s", string(data))), nil
}

type TaskPacketValidateTool struct {
	tools.BaseToolHelper
}

func NewTaskPacketValidateTool() *TaskPacketValidateTool {
	return &TaskPacketValidateTool{
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "TaskPacketValidate",
			ToolDescription: "Validate a TaskPacket for completeness and correctness.",
			ReadOnly:        true,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"packet": map[string]any{
						"type":        "object",
						"description": "The TaskPacket JSON object to validate.",
					},
				},
				"required": []string{"packet"},
			},
		},
	}
}

func (t *TaskPacketValidateTool) Execute(_ context.Context, input json.RawMessage, _ *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var wrapper struct {
		Packet tasks.TaskPacket `json:"packet"`
	}
	if err := json.Unmarshal(input, &wrapper); err != nil {
		return tools.NewToolResultError(fmt.Sprintf("invalid input: %v", err)), nil
	}

	var issues []string
	if wrapper.Packet.Objective == "" {
		issues = append(issues, "missing required field: objective")
	}
	if len(wrapper.Packet.Scope) == 0 {
		issues = append(issues, "warning: scope is empty — consider specifying target files/directories")
	}
	if len(wrapper.Packet.AcceptanceTests) == 0 {
		issues = append(issues, "warning: no acceptance_tests defined — consider adding validation commands")
	}

	if len(issues) == 0 {
		return tools.NewToolResult("TaskPacket is valid. All required fields are present."), nil
	}

	result := "Validation results:\n"
	for _, issue := range issues {
		result += fmt.Sprintf("  - %s\n", issue)
	}
	return tools.NewToolResult(result), nil
}
