package config

import (
	"os"
	"path/filepath"
)

// GetConfigDir returns the openharness configuration directory.
func GetConfigDir() string {
	if dir := os.Getenv("OPENHARNESS_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".openharness")
}

// GetConfigFilePath returns the path to the settings.json file.
func GetConfigFilePath() string {
	return filepath.Join(GetConfigDir(), "settings.json")
}

// GetDataDir returns the data/ subdirectory inside the config dir.
func GetDataDir() string {
	return filepath.Join(GetConfigDir(), "data")
}

// GetLogsDir returns the logs/ subdirectory.
func GetLogsDir() string {
	return filepath.Join(GetDataDir(), "logs")
}

// GetSessionsDir returns the sessions/ subdirectory.
func GetSessionsDir() string {
	return filepath.Join(GetDataDir(), "sessions")
}

// GetTasksDir returns the tasks/ subdirectory.
func GetTasksDir() string {
	return filepath.Join(GetDataDir(), "tasks")
}

// GetFeedbackDir returns the feedback/ subdirectory.
func GetFeedbackDir() string {
	return filepath.Join(GetDataDir(), "feedback")
}

// GetFeedbackLogPath returns the path to the feedback log file.
func GetFeedbackLogPath() string {
	return filepath.Join(GetFeedbackDir(), "feedback.log")
}

// GetCronRegistryPath returns the path to the cron registry file.
func GetCronRegistryPath() string {
	return filepath.Join(GetDataDir(), "cron_registry.json")
}

// GetProjectConfigDir returns the .openharness/ directory inside the given
// working directory (project root).
func GetProjectConfigDir(cwd string) string {
	return filepath.Join(cwd, ".openharness")
}

// GetProjectIssueFile returns the issue file path for a project.
func GetProjectIssueFile(cwd string) string {
	return filepath.Join(GetProjectConfigDir(cwd), "issue.md")
}

// GetProjectPRCommentsFile returns the PR comments file path for a project.
func GetProjectPRCommentsFile(cwd string) string {
	return filepath.Join(GetProjectConfigDir(cwd), "pr_comments.md")
}
