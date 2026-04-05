package engine

import (
	"context"
	"sync"

	"github.com/openharness/openharness/pkg/tools"
	"github.com/openharness/openharness/pkg/types"
)

// ---------------------------------------------------------------------------
// CostTracker – tracks cumulative token usage.
// ---------------------------------------------------------------------------

// CostTracker accumulates token usage across multiple LLM calls.
type CostTracker struct {
	mu           sync.Mutex
	InputTokens  int
	OutputTokens int
}

// Add records additional usage.
func (ct *CostTracker) Add(usage *types.UsageSnapshot) {
	if usage == nil {
		return
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.InputTokens += usage.InputTokens
	ct.OutputTokens += usage.OutputTokens
}

// Snapshot returns a copy of the current totals.
func (ct *CostTracker) Snapshot() types.UsageSnapshot {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return types.UsageSnapshot{
		InputTokens:  ct.InputTokens,
		OutputTokens: ct.OutputTokens,
	}
}

// ---------------------------------------------------------------------------
// QueryEngine – high-level engine that manages conversation state.
// ---------------------------------------------------------------------------

// QueryEngine wraps RunQuery with conversation state management.
type QueryEngine struct {
	mu sync.Mutex

	apiClient         StreamingLLMClient
	toolRegistry      *tools.ToolRegistry
	permissionChecker PermissionChecker
	cwd               string
	model             string
	systemPrompt      string
	maxTokens         int
	maxTurns          int
	hookExecutor      HookExecutor

	Messages    []types.ConversationMessage
	costTracker CostTracker
}

// QueryEngineOption is a functional option for NewQueryEngine.
type QueryEngineOption func(*QueryEngine)

// WithMaxTurns sets the maximum number of agent turns per submission.
func WithMaxTurns(n int) QueryEngineOption {
	return func(qe *QueryEngine) { qe.maxTurns = n }
}

// WithHookExecutor attaches a hook executor.
func WithHookExecutor(h HookExecutor) QueryEngineOption {
	return func(qe *QueryEngine) { qe.hookExecutor = h }
}

// WithPermissionChecker sets the permission checker.
func WithPermissionChecker(pc PermissionChecker) QueryEngineOption {
	return func(qe *QueryEngine) { qe.permissionChecker = pc }
}

// NewQueryEngine creates a QueryEngine with the given required dependencies.
func NewQueryEngine(
	apiClient StreamingLLMClient,
	toolRegistry *tools.ToolRegistry,
	cwd string,
	model string,
	systemPrompt string,
	maxTokens int,
	opts ...QueryEngineOption,
) *QueryEngine {
	qe := &QueryEngine{
		apiClient:         apiClient,
		toolRegistry:      toolRegistry,
		permissionChecker: AllowAllPermissions{},
		cwd:               cwd,
		model:             model,
		systemPrompt:      systemPrompt,
		maxTokens:         maxTokens,
		maxTurns:          8,
	}
	for _, o := range opts {
		o(qe)
	}
	return qe
}

// SubmitMessage appends a user message and runs the agent loop.
func (qe *QueryEngine) SubmitMessage(ctx context.Context, prompt string) <-chan StreamEventWithUsage {
	qe.mu.Lock()

	qe.Messages = append(qe.Messages, types.ConversationMessage{
		Role:    "user",
		Content: []types.ContentBlock{types.NewTextBlock(prompt)},
	})

	msgs := make([]types.ConversationMessage, len(qe.Messages))
	copy(msgs, qe.Messages)

	qctx := &QueryContext{
		APIClient:         qe.apiClient,
		ToolRegistry:      qe.toolRegistry,
		PermissionChecker: qe.permissionChecker,
		Cwd:               qe.cwd,
		Model:             qe.model,
		SystemPrompt:      qe.systemPrompt,
		MaxTokens:         qe.maxTokens,
		MaxTurns:          qe.maxTurns,
		HookExecutor:      qe.hookExecutor,
	}

	qe.mu.Unlock()

	rawCh := RunQuery(ctx, qctx, msgs)
	outCh := make(chan StreamEventWithUsage, 64)

	go func() {
		defer close(outCh)
		for ev := range rawCh {
			if ev.Usage != nil {
				qe.costTracker.Add(ev.Usage)
			}
			outCh <- ev
		}
		qe.mu.Lock()
		qe.Messages = msgs
		qe.mu.Unlock()
	}()

	return outCh
}

// Clear resets the conversation history.
func (qe *QueryEngine) Clear() {
	qe.mu.Lock()
	defer qe.mu.Unlock()
	qe.Messages = nil
}

// SetSystemPrompt updates the system prompt.
func (qe *QueryEngine) SetSystemPrompt(prompt string) {
	qe.mu.Lock()
	defer qe.mu.Unlock()
	qe.systemPrompt = prompt
}

// SetModel updates the model.
func (qe *QueryEngine) SetModel(model string) {
	qe.mu.Lock()
	defer qe.mu.Unlock()
	qe.model = model
}

// LoadMessages replaces the conversation history.
func (qe *QueryEngine) LoadMessages(messages []types.ConversationMessage) {
	qe.mu.Lock()
	defer qe.mu.Unlock()
	qe.Messages = make([]types.ConversationMessage, len(messages))
	copy(qe.Messages, messages)
}

// CostSnapshot returns the cumulative token usage.
func (qe *QueryEngine) CostSnapshot() types.UsageSnapshot {
	return qe.costTracker.Snapshot()
}
