package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openharness/openharness/pkg/engine"
	"github.com/openharness/openharness/pkg/tools"
	"github.com/openharness/openharness/pkg/types"
)

const defaultSubAgentMaxTurns = 32

type SubAgentConfig struct {
	Type         SubAgentType
	MaxTurns     int
	Prompt       string
	SystemPrompt string
	AllowedTools []string
}

type SubAgentExecutor struct {
	Registry     *TaskRegistry
	ToolRegistry *tools.ToolRegistry
	APIClient    engine.StreamingLLMClient
	Model        string
	MaxTokens    int
	Cwd          string
}

func (e *SubAgentExecutor) SpawnAgent(ctx context.Context, cfg SubAgentConfig) (string, error) {
	agentType := string(cfg.Type)
	if agentType == "" {
		agentType = string(SubAgentGeneral)
	}

	taskID, err := e.Registry.Create(cfg.Prompt, agentType, nil)
	if err != nil {
		return "", fmt.Errorf("create task entry: %w", err)
	}

	subCtx, cancel := context.WithCancel(ctx)
	e.Registry.setCancelFunc(taskID, cancel)
	_ = e.Registry.SetStatus(taskID, TaskRunning)

	go e.runAgent(subCtx, taskID, cfg)

	return taskID, nil
}

func (e *SubAgentExecutor) SpawnAgentSync(ctx context.Context, cfg SubAgentConfig) (string, string, error) {
	agentType := string(cfg.Type)
	if agentType == "" {
		agentType = string(SubAgentGeneral)
	}

	taskID, err := e.Registry.Create(cfg.Prompt, agentType, nil)
	if err != nil {
		return "", "", fmt.Errorf("create task entry: %w", err)
	}

	subCtx, cancel := context.WithCancel(ctx)
	e.Registry.setCancelFunc(taskID, cancel)
	_ = e.Registry.SetStatus(taskID, TaskRunning)

	e.runAgent(subCtx, taskID, cfg)

	output, _ := e.Registry.GetOutput(taskID)
	entry, _ := e.Registry.Get(taskID)
	if entry != nil && entry.Status == TaskFailed {
		return taskID, output, fmt.Errorf("agent failed: %s", entry.Error)
	}
	return taskID, output, nil
}

func (e *SubAgentExecutor) runAgent(ctx context.Context, taskID string, cfg SubAgentConfig) {
	defer func() {
		if r := recover(); r != nil {
			_ = e.Registry.SetStatus(taskID, TaskFailed)
			_ = e.Registry.AppendOutput(taskID, fmt.Sprintf("panic: %v", r))
		}
	}()

	sandboxReg := e.buildSandboxedRegistry(cfg.Type, cfg.AllowedTools)

	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultSubAgentMaxTurns
	}

	sysPrompt := cfg.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = buildSubAgentSystemPrompt(cfg.Type, cfg.Prompt, sandboxReg.ToAPISchema())
	}

	messages := []types.ConversationMessage{
		{
			Role:    "user",
			Content: []types.ContentBlock{types.NewTextBlock(cfg.Prompt)},
		},
	}

	qctx := &engine.QueryContext{
		APIClient:         e.APIClient,
		ToolRegistry:      sandboxReg,
		PermissionChecker: engine.AllowAllPermissions{},
		Cwd:               e.Cwd,
		Model:             e.Model,
		SystemPrompt:      sysPrompt,
		MaxTokens:         e.MaxTokens,
		MaxTurns:          maxTurns,
	}

	ch := engine.RunQuery(ctx, qctx, &messages)

	var resultBuilder strings.Builder
	for ev := range ch {
		if ev.Event.Error != nil {
			_ = e.Registry.SetStatus(taskID, TaskFailed)
			_ = e.Registry.SetError(taskID, ev.Event.Error.Error())
			_ = e.Registry.AppendOutput(taskID, fmt.Sprintf("Error: %v", ev.Event.Error))
			return
		}
		if ev.Event.Type == engine.EventTextDelta {
			resultBuilder.WriteString(ev.Event.Text)
		}
		if ev.Event.Type == engine.EventToolExecutionCompleted {
			_ = e.Registry.AppendOutput(taskID, fmt.Sprintf("[%s] %s", ev.Event.ToolName, truncate(ev.Event.ToolResult.Output, 500)))
		}
	}

	finalOutput := resultBuilder.String()
	_ = e.Registry.AppendOutput(taskID, finalOutput)
	_ = e.Registry.SetStatus(taskID, TaskCompleted)
}

func (e *SubAgentExecutor) buildSandboxedRegistry(agentType SubAgentType, explicitTools []string) *tools.ToolRegistry {
	allowed := explicitTools
	if len(allowed) == 0 {
		allowed = getToolWhitelist(agentType)
	}

	if len(allowed) == 0 {
		fullReg := tools.NewToolRegistry()
		for _, t := range e.ToolRegistry.ListTools() {
			if t.Name() == "Agent" {
				continue
			}
			_ = fullReg.Register(t)
		}
		return fullReg
	}

	sandbox := tools.NewToolRegistry()
	for _, name := range allowed {
		if t := e.ToolRegistry.Get(name); t != nil {
			_ = sandbox.Register(t)
		}
	}
	return sandbox
}

func getToolWhitelist(t SubAgentType) []string {
	switch t {
	case SubAgentExplore:
		return []string{"Read", "Glob", "Grep", "Bash", "LS"}
	case SubAgentPlan:
		return []string{"Read", "Glob", "Grep", "Bash"}
	case SubAgentVerification:
		return []string{"Read", "Glob", "Grep", "Bash"}
	default:
		return nil
	}
}

func buildSubAgentSystemPrompt(agentType SubAgentType, objective string, tools []map[string]any) string {
	role := "a general-purpose coding agent"
	switch agentType {
	case SubAgentExplore:
		role = "a code exploration agent. You have READ-ONLY access to the codebase"
	case SubAgentPlan:
		role = "a planning agent. Analyze the codebase and produce a detailed plan"
	case SubAgentVerification:
		role = "a verification agent. Run tests and validate code correctness"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("You are %s working on the following objective:\n\n<objective>\n%s\n</objective>\n\n", role, objective))
	
	if len(tools) > 0 {
		sb.WriteString("You have access to the following tools:\n<available_tools>\n")
		for _, t := range tools {
			name, _ := t["name"].(string)
			desc, _ := t["description"].(string)
			sb.WriteString(fmt.Sprintf("<tool>\n  <name>%s</name>\n  <description>%s</description>\n</tool>\n", name, desc))
		}
		sb.WriteString("</available_tools>\n\n")
	}

	sb.WriteString("Complete the objective using the tools available to you. Be thorough and systematic.\nWhen you are done, provide a clear summary of what you accomplished.")
	return sb.String()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

var _ json.Marshaler = (*TaskEntry)(nil)

func (e *TaskEntry) MarshalJSON() ([]byte, error) {
	type Alias TaskEntry
	return json.Marshal(&struct {
		*Alias
		CancelFunc interface{} `json:"cancel_func,omitempty"`
	}{
		Alias: (*Alias)(e),
	})
}
