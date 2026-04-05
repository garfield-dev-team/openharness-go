package permissions

import (
	"fmt"
	"path/filepath"

	"github.com/openharness/openharness/pkg/config"
)

// PermissionDecision is the result of checking whether a tool invocation may run.
type PermissionDecision struct {
	Allowed              bool
	RequiresConfirmation bool
	Reason               string
}

// PathRule is a glob-based path permission rule.
type PathRule struct {
	Pattern string
	Allow   bool
}

// PermissionChecker evaluates tool usage against the configured permission
// mode and rules.
type PermissionChecker struct {
	settings  config.PermissionSettings
	pathRules []PathRule
}

// NewPermissionChecker creates a checker from the given permission settings.
func NewPermissionChecker(s config.PermissionSettings) *PermissionChecker {
	rules := make([]PathRule, 0, len(s.PathRules))
	for _, r := range s.PathRules {
		if r.Pattern != "" {
			rules = append(rules, PathRule{Pattern: r.Pattern, Allow: r.Allow})
		}
	}
	return &PermissionChecker{settings: s, pathRules: rules}
}

// Evaluate determines whether the given tool invocation is permitted.
func (c *PermissionChecker) Evaluate(
	toolName string,
	isReadOnly bool,
	filePath string,
	command string,
) PermissionDecision {
	if contains(c.settings.DeniedTools, toolName) {
		return PermissionDecision{Allowed: false, Reason: fmt.Sprintf("%s is explicitly denied", toolName)}
	}
	if contains(c.settings.AllowedTools, toolName) {
		return PermissionDecision{Allowed: true, Reason: fmt.Sprintf("%s is explicitly allowed", toolName)}
	}
	if filePath != "" && len(c.pathRules) > 0 {
		for _, rule := range c.pathRules {
			matched, _ := filepath.Match(rule.Pattern, filePath)
			if matched && !rule.Allow {
				return PermissionDecision{Allowed: false, Reason: fmt.Sprintf("Path %s matches deny rule: %s", filePath, rule.Pattern)}
			}
		}
	}
	if command != "" {
		for _, pattern := range c.settings.DeniedCommands {
			if matched, _ := filepath.Match(pattern, command); matched {
				return PermissionDecision{Allowed: false, Reason: fmt.Sprintf("Command matches deny pattern: %s", pattern)}
			}
		}
	}

	mode := PermissionMode(c.settings.Mode)

	if mode == ModeFullAuto {
		return PermissionDecision{Allowed: true, Reason: "Auto mode allows all tools"}
	}
	if isReadOnly {
		return PermissionDecision{Allowed: true, Reason: "read-only tools are allowed"}
	}
	if mode == ModePlan {
		return PermissionDecision{Allowed: false, Reason: "Plan mode blocks mutating tools until the user exits plan mode"}
	}
	return PermissionDecision{Allowed: false, RequiresConfirmation: true, Reason: "Mutating tools require user confirmation in default mode"}
}

func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
