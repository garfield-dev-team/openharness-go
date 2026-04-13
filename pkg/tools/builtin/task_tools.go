package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openharness/openharness/pkg/tasks"
	"github.com/openharness/openharness/pkg/tools"
)

type TaskCreateTool struct {
	tools.BaseToolHelper
	executor *tasks.SubAgentExecutor
}

func NewTaskCreateTool(executor *tasks.SubAgentExecutor) *TaskCreateTool {
	return &TaskCreateTool{
		executor: executor,
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "TaskCreate",
			ToolDescription: "Create a new task and spawn a sub-agent to work on it. The sub-agent runs asynchronously. Use TaskGet to check its status later.",
			ReadOnly:        false,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "The objective/prompt for the sub-agent.",
					},
					"agent_type": map[string]any{
						"type":        "string",
						"description": "Type of sub-agent: general-purpose, Explore, Plan, Verification. Defaults to general-purpose.",
						"enum":        []string{"general-purpose", "Explore", "Plan", "Verification"},
					},
				},
				"required": []string{"prompt"},
			},
		},
	}
}

func (t *TaskCreateTool) Execute(ctx context.Context, input json.RawMessage, execCtx *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in struct {
		Prompt    string `json:"prompt"`
		AgentType string `json:"agent_type"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid TaskCreate input: %w", err)
	}
	if in.Prompt == "" {
		return tools.NewToolResultError("prompt is required"), nil
	}
	if in.AgentType == "" {
		in.AgentType = string(tasks.SubAgentGeneral)
	}

	taskID, err := t.executor.SpawnAgent(ctx, tasks.SubAgentConfig{
		Type:   tasks.SubAgentType(in.AgentType),
		Prompt: in.Prompt,
	})
	if err != nil {
		return tools.NewToolResultError(fmt.Sprintf("failed to create task: %v", err)), nil
	}

	return tools.NewToolResult(fmt.Sprintf("Task created successfully. task_id=%s, agent_type=%s. Use TaskGet to check status.", taskID, in.AgentType)), nil
}

type TaskGetTool struct {
	tools.BaseToolHelper
	registry *tasks.TaskRegistry
}

func NewTaskGetTool(registry *tasks.TaskRegistry) *TaskGetTool {
	return &TaskGetTool{
		registry: registry,
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "TaskGet",
			ToolDescription: "Get the current status and details of a task by its ID.",
			ReadOnly:        true,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "The ID of the task to query.",
					},
				},
				"required": []string{"task_id"},
			},
		},
	}
}

func (t *TaskGetTool) Execute(_ context.Context, input json.RawMessage, _ *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid TaskGet input: %w", err)
	}
	entry, err := t.registry.Get(in.TaskID)
	if err != nil {
		return tools.NewToolResultError(err.Error()), nil
	}
	data, _ := json.MarshalIndent(entry, "", "  ")
	return tools.NewToolResult(string(data)), nil
}

type TaskListTool struct {
	tools.BaseToolHelper
	registry *tasks.TaskRegistry
}

func NewTaskListTool(registry *tasks.TaskRegistry) *TaskListTool {
	return &TaskListTool{
		registry: registry,
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "TaskList",
			ToolDescription: "List all tasks with their current status.",
			ReadOnly:        true,
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (t *TaskListTool) Execute(_ context.Context, _ json.RawMessage, _ *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	entries := t.registry.List()
	if len(entries) == 0 {
		return tools.NewToolResult("No tasks found."), nil
	}
	type summary struct {
		ID        string             `json:"id"`
		Status    tasks.TaskStatus   `json:"status"`
		AgentType string             `json:"agent_type"`
		Prompt    string             `json:"prompt"`
	}
	summaries := make([]summary, len(entries))
	for i, e := range entries {
		prompt := e.Prompt
		if len(prompt) > 100 {
			prompt = prompt[:100] + "..."
		}
		summaries[i] = summary{
			ID:        e.ID,
			Status:    e.Status,
			AgentType: e.AgentType,
			Prompt:    prompt,
		}
	}
	data, _ := json.MarshalIndent(summaries, "", "  ")
	return tools.NewToolResult(string(data)), nil
}

type TaskStopTool struct {
	tools.BaseToolHelper
	registry *tasks.TaskRegistry
}

func NewTaskStopTool(registry *tasks.TaskRegistry) *TaskStopTool {
	return &TaskStopTool{
		registry: registry,
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "TaskStop",
			ToolDescription: "Stop a running task by its ID. This cancels the sub-agent's execution.",
			ReadOnly:        false,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "The ID of the task to stop.",
					},
				},
				"required": []string{"task_id"},
			},
		},
	}
}

func (t *TaskStopTool) Execute(_ context.Context, input json.RawMessage, _ *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid TaskStop input: %w", err)
	}
	if err := t.registry.Stop(in.TaskID); err != nil {
		return tools.NewToolResultError(err.Error()), nil
	}
	return tools.NewToolResult(fmt.Sprintf("Task %s stopped.", in.TaskID)), nil
}

type TaskOutputTool struct {
	tools.BaseToolHelper
	registry *tasks.TaskRegistry
}

func NewTaskOutputTool(registry *tasks.TaskRegistry) *TaskOutputTool {
	return &TaskOutputTool{
		registry: registry,
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "TaskOutput",
			ToolDescription: "Get the accumulated output of a task.",
			ReadOnly:        true,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "The ID of the task.",
					},
				},
				"required": []string{"task_id"},
			},
		},
	}
}

func (t *TaskOutputTool) Execute(_ context.Context, input json.RawMessage, _ *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid TaskOutput input: %w", err)
	}
	output, err := t.registry.GetOutput(in.TaskID)
	if err != nil {
		return tools.NewToolResultError(err.Error()), nil
	}
	if output == "" {
		return tools.NewToolResult("(no output yet)"), nil
	}
	return tools.NewToolResult(output), nil
}

type TaskSendMessageTool struct {
	tools.BaseToolHelper
	registry *tasks.TaskRegistry
}

func NewTaskSendMessageTool(registry *tasks.TaskRegistry) *TaskSendMessageTool {
	return &TaskSendMessageTool{
		registry: registry,
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "TaskSendMessage",
			ToolDescription: "Send a message to a running task's sub-agent.",
			ReadOnly:        false,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "The ID of the task.",
					},
					"message": map[string]any{
						"type":        "string",
						"description": "The message content to send.",
					},
				},
				"required": []string{"task_id", "message"},
			},
		},
	}
}

func (t *TaskSendMessageTool) Execute(_ context.Context, input json.RawMessage, _ *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in struct {
		TaskID  string `json:"task_id"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid TaskSendMessage input: %w", err)
	}
	msg := tasks.TaskMessage{
		From:    "main_agent",
		Content: in.Message,
	}
	if err := t.registry.SendMessage(in.TaskID, msg); err != nil {
		return tools.NewToolResultError(err.Error()), nil
	}
	return tools.NewToolResult("Message sent."), nil
}

type TaskUpdateTool struct {
	tools.BaseToolHelper
	registry *tasks.TaskRegistry
}

func NewTaskUpdateTool(registry *tasks.TaskRegistry) *TaskUpdateTool {
	return &TaskUpdateTool{
		registry: registry,
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "TaskUpdate",
			ToolDescription: "Update the status of a task manually.",
			ReadOnly:        false,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "The ID of the task.",
					},
					"status": map[string]any{
						"type":        "string",
						"description": "The new status for the task.",
						"enum":        []string{"completed", "failed", "stopped"},
					},
				},
				"required": []string{"task_id", "status"},
			},
		},
	}
}

func (t *TaskUpdateTool) Execute(_ context.Context, input json.RawMessage, _ *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid TaskUpdate input: %w", err)
	}
	if err := t.registry.SetStatus(in.TaskID, tasks.TaskStatus(in.Status)); err != nil {
		return tools.NewToolResultError(err.Error()), nil
	}
	return tools.NewToolResult(fmt.Sprintf("Task %s status updated to %s.", in.TaskID, in.Status)), nil
}
