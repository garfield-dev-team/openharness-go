// Package ui provides the application runtime assembly and REPL/print-mode entry points.
package ui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/openharness/openharness/pkg/api"
	"github.com/openharness/openharness/pkg/config"
	"github.com/openharness/openharness/pkg/engine"
	"github.com/openharness/openharness/pkg/hooks"
	"github.com/openharness/openharness/pkg/mcp"
	"github.com/openharness/openharness/pkg/memory"
	"github.com/openharness/openharness/pkg/prompts"
	"github.com/openharness/openharness/pkg/skills"
	"github.com/openharness/openharness/pkg/state"
	"github.com/openharness/openharness/pkg/tools"
	"github.com/openharness/openharness/pkg/tools/builtin"
)

// RuntimeBundle holds all the wired-up components for a session.
type RuntimeBundle struct {
	APIClient    api.MessageStreamer
	MCPManager   *mcp.McpClientManager
	ToolRegistry *tools.ToolRegistry
	AppState     *state.AppStateStore
	HookExecutor *hooks.HookExecutor
	Engine       *engine.QueryEngine
	SessionID    string
	Cwd          string
}

// BuildRuntime assembles a RuntimeBundle from settings and cwd.
func BuildRuntime(settings *config.Settings, cwd string) (*RuntimeBundle, error) {
	apiKey, err := settings.ResolveAPIKey()
	if err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}

	baseURL := ""
	if settings.BaseURL != nil {
		baseURL = *settings.BaseURL
	}

	providerInfo := api.DetectProvider(*settings)

	var apiClient api.MessageStreamer
	if providerInfo.Name == "openai-compatible" {
		apiClient = api.NewOpenAIApiClient(apiKey, baseURL)
	} else {
		apiClient = api.NewAnthropicApiClient(apiKey, baseURL)
	}

	toolReg := builtin.CreateDefaultToolRegistry()

	// Load skills from ~/.openharness/skills and ./skills
	var loadedSkills []skills.Skill
	if home, err := os.UserHomeDir(); err == nil {
		globalSkills, _ := skills.LoadSkills(fmt.Sprintf("%s/.openharness/skills", home))
		loadedSkills = append(loadedSkills, globalSkills...)
	}
	localSkills, _ := skills.LoadSkills(fmt.Sprintf("%s/skills", cwd))
	loadedSkills = append(loadedSkills, localSkills...)

	if len(loadedSkills) > 0 {
		toolReg.Register(builtin.NewSkillTool(loadedSkills))
	}

	mcpConfigs := make(map[string]mcp.McpServerConfig)
	mcpMgr := mcp.NewMcpClientManager(mcpConfigs)

	hookReg := hooks.NewHookRegistry()
	hookExecCtx := &hooks.HookExecutionContext{
		Cwd:          cwd,
		DefaultModel: settings.Model,
	}
	hookExec := hooks.NewHookExecutor(hookReg, hookExecCtx)

	appState := state.NewAppStateStore(state.AppState{
		Model:          settings.Model,
		PermissionMode: string(settings.Permission.Mode),
		Theme:          settings.Theme,
		Cwd:            cwd,
		Provider:       providerInfo.Name,
		AuthStatus:     api.AuthStatus(*settings),
		BaseURL:        baseURL,
		VimEnabled:     settings.VimMode,
		VoiceEnabled:   settings.VoiceMode,
		FastMode:       settings.FastMode,
		Effort:         settings.Effort,
		Passes:         settings.Passes,
		OutputStyle:    settings.OutputStyle,
	})

	memoryPrompt := memory.LoadMemoryPrompt(cwd)
	claudeMDPaths := prompts.DiscoverClaudeMD(cwd)
	claudeMDContent := ""
	if len(claudeMDPaths) > 0 {
		if data, readErr := os.ReadFile(claudeMDPaths[0]); readErr == nil {
			claudeMDContent = string(data)
		}
	}
	sysPrompt := prompts.BuildRuntimeSystemPrompt("", cwd, memoryPrompt, loadedSkills, claudeMDContent)
	if settings.SystemPrompt != nil && *settings.SystemPrompt != "" {
		sysPrompt = *settings.SystemPrompt
	}

	adapter := &apiClientAdapter{client: apiClient, model: settings.Model}
	qe := engine.NewQueryEngine(
		adapter,
		toolReg,
		cwd,
		settings.Model,
		sysPrompt,
		settings.MaxTokens,
	)

	sessionID := fmt.Sprintf("session_%d", time.Now().UnixMilli())

	return &RuntimeBundle{
		APIClient:    apiClient,
		MCPManager:   mcpMgr,
		ToolRegistry: toolReg,
		AppState:     appState,
		HookExecutor: hookExec,
		Engine:       qe,
		SessionID:    sessionID,
		Cwd:          cwd,
	}, nil
}

// Start connects MCP servers and performs other async initialisation.
func (r *RuntimeBundle) Start(ctx context.Context) error {
	if err := r.MCPManager.ConnectAll(ctx); err != nil {
		return fmt.Errorf("runtime: mcp connect: %w", err)
	}
	statuses := r.MCPManager.ListStatuses()
	connected, failed := 0, 0
	for _, s := range statuses {
		if s.State == mcp.StateConnected {
			connected++
		} else if s.State == mcp.StateFailed {
			failed++
		}
	}
	r.AppState.Update(func(s *state.AppState) {
		s.McpConnected = connected
		s.McpFailed = failed
	})
	return nil
}

// Close shuts down MCP connections and releases resources.
func (r *RuntimeBundle) Close() error {
	r.MCPManager.Close()
	return nil
}

// HandleLine processes a single user input line through the engine.
func (r *RuntimeBundle) HandleLine(ctx context.Context, line string) error {
	ch := r.Engine.SubmitMessage(ctx, line)
	for ev := range ch {
		if ev.Event.Error != nil {
			return ev.Event.Error
		}
		if ev.Event.Type == engine.EventTextDelta {
			fmt.Print(ev.Event.Text)
		}
	}
	fmt.Println()
	return nil
}
