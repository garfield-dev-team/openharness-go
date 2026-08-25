// Package ui provides the application runtime assembly and REPL/print-mode entry points.
package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openharness/openharness/pkg/api"
	"github.com/openharness/openharness/pkg/config"
	"github.com/openharness/openharness/pkg/engine"
	"github.com/openharness/openharness/pkg/hooks"
	"github.com/openharness/openharness/pkg/logger"
	"github.com/openharness/openharness/pkg/mcp"
	"github.com/openharness/openharness/pkg/memory"
	"github.com/openharness/openharness/pkg/prompts"
	"github.com/openharness/openharness/pkg/services"
	"github.com/openharness/openharness/pkg/session"
	"github.com/openharness/openharness/pkg/skills"
	"github.com/openharness/openharness/pkg/state"
	"github.com/openharness/openharness/pkg/tasks"
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
	Store        *session.Store
	SessionID    string
	Cwd          string
	Logger       logger.Logger
	Settings     *config.Settings

	adapter          *apiClientAdapter
	subAgentExecutor *tasks.SubAgentExecutor
}

type RuntimeOption func(*runtimeConfig)

type runtimeConfig struct {
	askUser       tools.AskUserFunc
	askPermission tools.AskPermissionFunc
	resumeID      string
	continueLast  bool
	output        logger.Logger
}

func WithHITLCallbacks(askUser tools.AskUserFunc, askPermission tools.AskPermissionFunc) RuntimeOption {
	return func(cfg *runtimeConfig) {
		cfg.askUser = askUser
		cfg.askPermission = askPermission
	}
}

// WithResumeSession opens the stored session tree for the given ID and seeds
// the conversation from its active branch.
func WithResumeSession(id string) RuntimeOption {
	return func(cfg *runtimeConfig) { cfg.resumeID = id }
}

// WithContinueLastSession resumes the most recently modified session.
func WithContinueLastSession() RuntimeOption {
	return func(cfg *runtimeConfig) { cfg.continueLast = true }
}

// WithLogger overrides the terminal output destination. When nil, BuildRuntime
// creates a Logger writing to os.Stdout at a level derived from settings.Verbose.
func WithLogger(l logger.Logger) RuntimeOption {
	return func(cfg *runtimeConfig) { cfg.output = l }
}

// BuildRuntime assembles a RuntimeBundle from settings and cwd.
func BuildRuntime(settings *config.Settings, cwd string, opts ...RuntimeOption) (*RuntimeBundle, error) {
	var cfg runtimeConfig
	for _, o := range opts {
		o(&cfg)
	}

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
	switch providerInfo.Name {
	case "openai-compatible", "opencode":
		apiClient = api.NewOpenAIApiClient(apiKey, baseURL)
	default:
		apiClient = api.NewAnthropicApiClient(apiKey, baseURL)
	}

	toolReg := builtin.CreateDefaultToolRegistry()

	// Load global plugins and skills
	// Load skills from ~/.openharness/skills and ./skills
	var loadedSkills []skills.Skill
	if home, err := os.UserHomeDir(); err == nil {
		globalPlugins, _ := skills.LoadPlugins(fmt.Sprintf("%s/.openharness/plugins", home))
		globalSkills, _ := skills.LoadSkills(fmt.Sprintf("%s/.openharness/skills", home))
		loadedSkills = append(loadedSkills, globalPlugins...)
		loadedSkills = append(loadedSkills, globalSkills...)
	}

	// Load local plugins and skills
	localPlugins, _ := skills.LoadPlugins(fmt.Sprintf("%s/plugins", cwd))
	localSkills, _ := skills.LoadSkills(fmt.Sprintf("%s/skills", cwd))
	loadedSkills = append(loadedSkills, localPlugins...)
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
	var customSysPrompt string
	if settings.SystemPrompt != nil && *settings.SystemPrompt != "" {
		customSysPrompt = *settings.SystemPrompt
	}
	sysPrompt := prompts.BuildRuntimeSystemPrompt(customSysPrompt, cwd, memoryPrompt, loadedSkills, toolReg.ToAPISchema(), claudeMDContent)

	adapter := &apiClientAdapter{client: apiClient, model: settings.Model}

	taskRegistry := tasks.NewTaskRegistry()
	subAgentExecutor := &tasks.SubAgentExecutor{
		Registry:     taskRegistry,
		ToolRegistry: toolReg,
		APIClient:    adapter,
		Model:        settings.Model,
		MaxTokens:    settings.MaxTokens,
		Cwd:          cwd,
	}

	_ = toolReg.Register(builtin.NewAgentTool(subAgentExecutor))
	_ = toolReg.Register(builtin.NewTaskCreateTool(subAgentExecutor))
	_ = toolReg.Register(builtin.NewTaskGetTool(taskRegistry))
	_ = toolReg.Register(builtin.NewTaskListTool(taskRegistry))
	_ = toolReg.Register(builtin.NewTaskStopTool(taskRegistry))
	_ = toolReg.Register(builtin.NewTaskOutputTool(taskRegistry))
	_ = toolReg.Register(builtin.NewTaskSendMessageTool(taskRegistry))
	_ = toolReg.Register(builtin.NewTaskUpdateTool(taskRegistry))
	_ = toolReg.Register(builtin.NewTaskPacketCreateTool())
	_ = toolReg.Register(builtin.NewTaskPacketValidateTool())

	var engineOpts []engine.QueryEngineOption
	if cfg.askUser != nil {
		engineOpts = append(engineOpts, engine.WithAskUser(cfg.askUser))
	}
	if cfg.askPermission != nil {
		engineOpts = append(engineOpts, engine.WithAskPermission(cfg.askPermission))
	}
	compactionCfg := services.ResolveCompactionConfig(settings.Model, settings.ContextWindow, settings.CompactionThreshold)
	engineOpts = append(engineOpts, engine.WithCompactionConfig(compactionCfg))

	// Resolve the durable session tree: resume an existing file, continue
	// the most recent one, or start a fresh session under .openharness/sessions.
	sessionID := fmt.Sprintf("session_%d", time.Now().UnixMilli())
	var sess *session.Store
	switch {
	case cfg.resumeID != "":
		if !session.Exists(cwd, cfg.resumeID) {
			return nil, fmt.Errorf("runtime: session %q not found", cfg.resumeID)
		}
		sessionID = cfg.resumeID
	case cfg.continueLast:
		ids, err := session.ListIDs(cwd)
		if err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			sessionID = ids[0]
		}
	}
	sess, err = session.Open(session.FilePath(cwd, sessionID))
	if err != nil {
		return nil, err
	}
	engineOpts = append(engineOpts, engine.WithSessionStore(sess))

	qe := engine.NewQueryEngine(
		adapter,
		toolReg,
		cwd,
		settings.Model,
		sysPrompt,
		settings.MaxTokens,
		engineOpts...,
	)
	if sess.Len() > 0 {
		qe.LoadMessages(sess.ActiveMessages())
	}

	outLogger := cfg.output
	if outLogger == nil {
		outLogger = logger.New(os.Stdout, logger.LevelFromVerbose(settings.Verbose))
	}
	// Diagnostic logs (retries, SSE debug) go to stderr via the global logger.
	logger.SetDefault(logger.New(os.Stderr, logger.LevelFromVerbose(settings.Verbose)))

	return &RuntimeBundle{
		APIClient:        apiClient,
		MCPManager:       mcpMgr,
		ToolRegistry:     toolReg,
		AppState:         appState,
		HookExecutor:     hookExec,
		Engine:           qe,
		Store:            sess,
		SessionID:        sessionID,
		Cwd:              cwd,
		Logger:           outLogger,
		Settings:         settings,
		adapter:          adapter,
		subAgentExecutor: subAgentExecutor,
	}, nil
}

// SwitchModel updates the active model (and associated provider/baseURL,
// compaction threshold, and sub-agent) without restarting the session.
// When persist is true the new model is written to the settings file.
func (r *RuntimeBundle) SwitchModel(newModel string, persist bool) error {
	newModel = strings.TrimSpace(newModel)
	if newModel == "" {
		return fmt.Errorf("model must not be empty")
	}
	lower := strings.ToLower(newModel)
	// Auto-provider for the free OpenCode tier.
	if strings.HasPrefix(lower, "muse-spark") {
		r.Settings.Provider = "opencode"
		base := "https://opencode.ai/zen/v1"
		r.Settings.BaseURL = &base
	} else if r.Settings.Provider == "opencode" {
		// Leaving the opencode realm — clear the free-tier wiring.
		r.Settings.Provider = ""
		r.Settings.BaseURL = nil
	}
	r.Settings.Model = newModel

	// Validate auth (opencode is exempt).
	if _, err := r.Settings.ResolveAPIKey(); err != nil {
		return err
	}

	// Recreate the API client if provider/base changed.
	providerInfo := api.DetectProvider(*r.Settings)
	baseURL := ""
	if r.Settings.BaseURL != nil {
		baseURL = *r.Settings.BaseURL
	}
	apiKey, _ := r.Settings.ResolveAPIKey()
	var newClient api.MessageStreamer
	switch providerInfo.Name {
	case "openai-compatible", "opencode":
		newClient = api.NewOpenAIApiClient(apiKey, baseURL)
	default:
		newClient = api.NewAnthropicApiClient(apiKey, baseURL)
	}
	r.APIClient = newClient
	if r.adapter != nil {
		r.adapter.client = newClient
	}
	if r.subAgentExecutor != nil {
		r.subAgentExecutor.Model = newModel
		r.subAgentExecutor.APIClient = r.adapter
	}

	// Engine model + compaction (model-aware threshold).
	r.Engine.SetModel(newModel)
	r.Engine.SetCompactionConfig(services.ResolveCompactionConfig(newModel, r.Settings.ContextWindow, r.Settings.CompactionThreshold))

	// AppState for TUI header.
	r.AppState.Update(func(s *state.AppState) {
		s.Model = newModel
		s.Provider = providerInfo.Name
		s.BaseURL = baseURL
		s.AuthStatus = api.AuthStatus(*r.Settings)
	})

	if persist {
		if err := config.SaveSettings(*r.Settings); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
	}
	return nil
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
	return r.Store.Close()
}

// HandleLine processes a single user input line through the engine.
func (r *RuntimeBundle) HandleLine(ctx context.Context, line string) error {
	ch := r.Engine.SubmitMessage(ctx, line)

	out := r.Logger
	if out == nil {
		out = logger.New(os.Stdout, logger.LevelInfo)
	}
	isThinking := false
	clearThinking := func() {
		if isThinking {
			out.Print("\033[2K\r") // Clear the entire line and return to start
			isThinking = false
		}
	}
	rendered := false

	for ev := range ch {
		if ev.Event.Error != nil {
			clearThinking()
			return ev.Event.Error
		}
		switch ev.Event.Type {
		case engine.EventModelTurnStarted:
			out.Print("\033[90m⏳ Thinking...\033[0m")
			isThinking = true
		case engine.EventAborted:
			clearThinking()
			out.Println("\n\033[33m⏹ Aborted (session preserved)\033[0m")
		case engine.EventTextDelta:
			clearThinking()
			rendered = true
			out.Print(ev.Event.Text)
		case engine.EventReasoningDelta:
			clearThinking()
			rendered = true
			// 90 is dark gray: keep reasoning visible but visually subordinate.
			out.Print("\033[90m" + ev.Event.Text + "\033[0m")
		case engine.EventToolExecutionStarted:
			clearThinking()
			rendered = true
			argsStr := string(ev.Event.ToolInput)
			if len(argsStr) > 200 {
				argsStr = argsStr[:200] + "..."
			}
			// ANSI colors: 90 is dark gray (dim), 33 is yellow
			out.Printf("\n\033[90m▶ \033[33m%s\033[90m(%s)\033[0m\n", ev.Event.ToolName, argsStr)
		case engine.EventToolExecutionCompleted:
			clearThinking()
			if ev.Event.ToolResult != nil && ev.Event.ToolResult.IsError {
				// 31 is red
				out.Printf("\033[90m✖ \033[31m%s\033[90m failed\033[0m\n", ev.Event.ToolName)
			} else {
				// 32 is green
				out.Printf("\033[90m✔ \033[32m%s\033[90m completed\033[0m\n", ev.Event.ToolName)
			}
		case engine.EventAssistantTurnComplete:
			clearThinking()
			// Optionally print a newline or separator
		}
	}
	out.Println()

	if !rendered {
		out.Println("\033[33m⚠ Model returned no visible content (reasoning-only or empty response).\033[0m")
	}

	currentTokens := r.Engine.CurrentTokens()
	threshold := r.Engine.CompactionThreshold()
	pct := float64(currentTokens) / float64(threshold) * 100

	color := "\033[32m" // green
	if pct > 80 {
		color = "\033[31m" // red
	} else if pct > 50 {
		color = "\033[33m" // yellow
	}

	out.Printf("\n\033[90m[🧠 Brain Capacity] %s%.1f%%\033[90m (%d / %d tokens)\033[0m\n", color, pct, currentTokens, threshold)

	return nil
}
