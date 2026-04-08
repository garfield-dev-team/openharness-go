// Package prompts builds system prompts, environment info, and CLAUDE.md discovery.
package prompts

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/openharness/openharness/pkg/skills"
)

// EnvironmentInfo mirrors Python prompts/environment.py.
type EnvironmentInfo struct {
	OS        string
	Shell     string
	HomeDir   string
	Cwd       string
	GitBranch string
	GitRepo   string
}

// GetEnvironmentInfo gathers runtime environment data for the given cwd.
func GetEnvironmentInfo(cwd string) *EnvironmentInfo {
	info := &EnvironmentInfo{
		OS:  runtime.GOOS,
		Cwd: cwd,
	}

	if home, err := os.UserHomeDir(); err == nil {
		info.HomeDir = home
	}

	if shell := os.Getenv("SHELL"); shell != "" {
		info.Shell = filepath.Base(shell)
	} else {
		info.Shell = "unknown"
	}

	// Git branch
	if out, err := exec.Command("git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		info.GitBranch = strings.TrimSpace(string(out))
	}

	// Git repo root (used as the "repo" identifier)
	if out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output(); err == nil {
		info.GitRepo = filepath.Base(strings.TrimSpace(string(out)))
	}

	return info
}

// BuildSystemPrompt constructs the base system prompt from environment info.
func BuildSystemPrompt(envInfo *EnvironmentInfo) string {
	var sb strings.Builder
	sb.WriteString("You are an expert AI coding assistant working in an interactive CLI environment.\n")
	sb.WriteString("You have access to tools for executing bash commands, reading/writing files, and searching code.\n\n")
	
	sb.WriteString("# Key Guidelines\n")
	sb.WriteString("- Use `Bash` for running commands, tests, builds, and installations.\n")
	sb.WriteString("- Use `FileRead` to inspect existing code before making changes.\n")
	sb.WriteString("- Use `FileWrite` or `FileEdit` to create or modify files.\n")
	sb.WriteString("- Always verify your changes work by running relevant tests.\n")
	sb.WriteString("- Be concise in explanations, thorough in implementation.\n")
	sb.WriteString("- When encountering errors, diagnose the root cause before attempting fixes.\n")
	sb.WriteString("- You can delegate complex subtasks to a sub-agent using the `Agent` tool (if available).\n\n")

	sb.WriteString("# Environment\n")
	sb.WriteString(fmt.Sprintf("- OS: %s\n", envInfo.OS))
	sb.WriteString(fmt.Sprintf("- Shell: %s\n", envInfo.Shell))
	sb.WriteString(fmt.Sprintf("- Home: %s\n", envInfo.HomeDir))
	sb.WriteString(fmt.Sprintf("- CWD: %s\n", envInfo.Cwd))
	if envInfo.GitRepo != "" {
		sb.WriteString(fmt.Sprintf("- Git repo: %s\n", envInfo.GitRepo))
	}
	if envInfo.GitBranch != "" {
		sb.WriteString(fmt.Sprintf("- Git branch: %s\n", envInfo.GitBranch))
	}
	return sb.String()
}

// DiscoverClaudeMD searches for CLAUDE.md files starting at cwd and walking
// up to the filesystem root, returning all found paths (deepest first).
func DiscoverClaudeMD(cwd string) []string {
	var paths []string
	dir := cwd
	for {
		candidate := filepath.Join(dir, "CLAUDE.md")
		if _, err := os.Stat(candidate); err == nil {
			paths = append(paths, candidate)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return paths
}

// BuildRuntimeSystemPrompt assembles the full runtime system prompt from all
// constituent parts: user-level settings, cwd context, memory, skills, and
// CLAUDE.md content.
func BuildRuntimeSystemPrompt(settings, cwd, memory string, loadedSkills []skills.Skill, claudemd string) string {
	var sb strings.Builder

	envInfo := GetEnvironmentInfo(cwd)
	sb.WriteString(BuildSystemPrompt(envInfo))

	if settings != "" {
		sb.WriteString("\n# User Settings\n")
		sb.WriteString(settings)
		sb.WriteString("\n")
	}

	if memory != "" {
		sb.WriteString("\n# Memory\n")
		sb.WriteString(memory)
		sb.WriteString("\n")
	}

	if len(loadedSkills) > 0 {
		sb.WriteString("\n# Available Skills\n")
		sb.WriteString("You have access to specialized skills. To invoke a skill, use the `Skill` tool with the exact name.\n")
		sb.WriteString("<available_skills>\n")
		for _, s := range loadedSkills {
			sb.WriteString(fmt.Sprintf("<skill>\n  <name>%s</name>\n  <description>%s</description>\n</skill>\n", s.Name, s.Description))
		}
		sb.WriteString("</available_skills>\n")
	}

	if claudemd != "" {
		sb.WriteString("\n# Project Instructions (CLAUDE.md)\n")
		sb.WriteString(claudemd)
		sb.WriteString("\n")
	}

	return sb.String()
}
