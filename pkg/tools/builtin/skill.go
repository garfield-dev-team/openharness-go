package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openharness/openharness/pkg/skills"
	"github.com/openharness/openharness/pkg/tools"
)

// ---------------------------------------------------------------------------
// SkillTool – Load full instructions for a specific skill.
// ---------------------------------------------------------------------------

// SkillInput is the expected JSON input for SkillTool.
type SkillInput struct {
	Name string `json:"name"`
}

// SkillTool provides progressive disclosure of skills to the LLM.
type SkillTool struct {
	tools.BaseToolHelper
	skillsMap map[string]skills.Skill
}

// NewSkillTool creates a SkillTool instance with the loaded skills.
func NewSkillTool(loadedSkills []skills.Skill) *SkillTool {
	m := make(map[string]skills.Skill)
	for _, s := range loadedSkills {
		m[s.Name] = s
	}

	return &SkillTool{
		skillsMap: m,
		BaseToolHelper: tools.BaseToolHelper{
			ToolName:        "Skill",
			ToolDescription: "Execute a skill within the main conversation. Use this when you want to use a capability listed in <available_skills>. The skill's prompt will expand and provide detailed instructions.",
			ReadOnly:        true,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "The name of the skill to invoke.",
					},
				},
				"required": []string{"name"},
			},
		},
	}
}

// Execute returns the instructions for the requested skill.
func (t *SkillTool) Execute(_ context.Context, input json.RawMessage, execCtx *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var in SkillInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid SkillTool input: %w", err)
	}

	if in.Name == "" {
		return tools.NewToolResultError("name is required"), nil
	}

	skill, ok := t.skillsMap[in.Name]
	if !ok {
		return tools.NewToolResultError(fmt.Sprintf("Skill '%s' not found. Please check <available_skills> for valid names.", in.Name)), nil
	}

	output := fmt.Sprintf("<command-message>The \"%s\" skill is loading</command-message>\n\n<instructions>\n%s\n</instructions>\n", skill.Name, skill.Instructions)
	return tools.NewToolResult(output), nil
}
