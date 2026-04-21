package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openharness/openharness/pkg/tools"
)

type askUserQuestionTool struct {
	tools.BaseToolHelper
}

type askUserInput struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

func NewAskUserQuestionTool() tools.BaseTool {
	return &askUserQuestionTool{
		BaseToolHelper: tools.BaseToolHelper{
			ToolName: "ask_user_question",
			ToolDescription: `Ask the user a question and wait for their response. Use this tool when you need clarification, confirmation, or input from the user to proceed with a task.

You can ask:
- Free-form questions: The user types a text answer.
- Multiple-choice questions: Provide options and the user picks one.

Guidelines:
- Only ask when you genuinely need user input to proceed.
- Be specific and concise in your questions.
- For multiple-choice, provide clear and distinct options.
- Do not use this tool for rhetorical questions.`,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question": map[string]any{
						"type":        "string",
						"description": "The question to ask the user.",
					},
					"options": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional list of choices for a multiple-choice question.",
					},
				},
				"required": []string{"question"},
			},
			ReadOnly: true,
		},
	}
}

func (t *askUserQuestionTool) Execute(ctx context.Context, input json.RawMessage, execCtx *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var params askUserInput
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.NewToolResultError(fmt.Sprintf("invalid input: %v", err)), nil
	}
	if strings.TrimSpace(params.Question) == "" {
		return tools.NewToolResultError("question must not be empty"), nil
	}
	if execCtx.AskUser == nil {
		return tools.NewToolResultError(
			"ask_user_question is unavailable in this session (no interactive frontend connected)",
		), nil
	}

	answer, err := execCtx.AskUser(ctx, params.Question, params.Options)
	if err != nil {
		return tools.NewToolResultError(fmt.Sprintf("failed to get user response: %v", err)), nil
	}

	answer = strings.TrimSpace(answer)
	if answer == "" {
		return tools.NewToolResult("(no response from user)"), nil
	}
	return tools.NewToolResult(answer), nil
}