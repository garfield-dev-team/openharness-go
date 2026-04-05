// Package prompts builds system prompts, environment info, and CLAUDE.md discovery.
package prompts

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
	sb.WriteString("You are an AI coding assistant.\n\n")
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
func BuildRuntimeSystemPrompt(settings, cwd, memory, skills, claudemd string) string {
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

	if skills != "" {
		sb.WriteString("\n# Skills\n")
		sb.WriteString(skills)
		sb.WriteString("\n")
	}

	if claudemd != "" {
		sb.WriteString("\n# Project Instructions (CLAUDE.md)\n")
		sb.WriteString(claudemd)
		sb.WriteString("\n")
	}

	return sb.String()
}
