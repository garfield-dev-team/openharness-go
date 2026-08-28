package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/openharness/openharness/pkg/services"
	"github.com/openharness/openharness/pkg/session"
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
// Submissions – single-flight queue with pi-style delivery semantics.
// ---------------------------------------------------------------------------

// SubmissionKind selects where a submission joins the conversation.
type SubmissionKind string

const (
	// SubmissionNewTurn starts a fresh turn when the engine is idle.
	SubmissionNewTurn SubmissionKind = "new_turn"
	// SubmissionSteering is injected at the next delivery point inside the
	// running loop (after tool results, before the next LLM call). If the
	// engine is idle it behaves like SubmissionNewTurn.
	SubmissionSteering SubmissionKind = "steering"
	// SubmissionFollowUp is delivered as the next user turn after the
	// running loop finishes.
	SubmissionFollowUp SubmissionKind = "follow_up"
)

// Submission is one queued unit of user input.
type Submission struct {
	Kind   SubmissionKind
	Prompt string
}

type queuedSubmission struct {
	sub Submission
	ch  chan StreamEventWithUsage
}

// ---------------------------------------------------------------------------
// QueryEngine – high-level engine that manages conversation state.
// ---------------------------------------------------------------------------

// QueryEngine wraps RunQuery with conversation state management and a
// single-flight submission queue: at most one agent loop runs at any time;
// new input queues and is consumed at defined delivery points instead of
// running concurrent loops that overwrite each other's history.
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
	askUser           tools.AskUserFunc
	askPermission     tools.AskPermissionFunc
	store             *session.Store
	compactionConfig  *services.CompactionConfig

	Messages       []types.ConversationMessage
	collapseBuffer []types.ConversationMessage
	costTracker    CostTracker

	// Single-flight state. running guards runner goroutine startup; baseCtx
	// is derived into each run's cancellable context so Cancel aborts the
	// current query without poisoning the session.
	baseCtx       context.Context
	running       bool
	runCancel     context.CancelFunc
	turnQueue     []queuedSubmission // FIFO of new_turn / follow_up
	steeringQueue []queuedSubmission // FIFO of steering, drained at delivery point A
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

// WithAskUser sets the callback for asking the user questions.
func WithAskUser(fn tools.AskUserFunc) QueryEngineOption {
	return func(qe *QueryEngine) { qe.askUser = fn }
}

// WithAskPermission sets the callback for requesting user permission.
func WithAskPermission(fn tools.AskPermissionFunc) QueryEngineOption {
	return func(qe *QueryEngine) { qe.askPermission = fn }
}

// WithSessionStore mirrors every conversation change into a durable session
// tree as messages are appended.
func WithSessionStore(store *session.Store) QueryEngineOption {
	return func(qe *QueryEngine) { qe.store = store }
}

// WithCompactionConfig overrides the compaction config (threshold, snip
// budgets, etc.). When nil, a model-aware default is resolved on demand.
func WithCompactionConfig(cfg *services.CompactionConfig) QueryEngineOption {
	return func(qe *QueryEngine) { qe.compactionConfig = cfg }
}

// SetCompactionConfig replaces the compaction config at runtime (e.g. after
// a model switch).
func (qe *QueryEngine) SetCompactionConfig(cfg *services.CompactionConfig) {
	qe.mu.Lock()
	defer qe.mu.Unlock()
	qe.compactionConfig = cfg
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
		maxTurns:          100,
		baseCtx:           context.Background(),
		compactionConfig:  services.ResolveCompactionConfig(model, 0, 0),
	}
	for _, o := range opts {
		o(qe)
	}
	return qe
}

// Submit queues a submission and returns its event channel. The channel
// yields EventQueued immediately; EventDelivered when the submission enters
// the conversation (for steering this happens at delivery point A), and
// EventUndelivered if the running query aborted before delivery. Every
// channel is closed exactly when nothing more will be sent on it.
//
// The returned channel carries only lifecycle events for steering
// submissions; their effect is visible in the owning run's event stream.
func (qe *QueryEngine) Submit(kind SubmissionKind, prompt string) <-chan StreamEventWithUsage {
	qs := queuedSubmission{
		sub: Submission{Kind: kind, Prompt: prompt},
		ch:  make(chan StreamEventWithUsage, 16),
	}
	qs.ch <- StreamEventWithUsage{Event: StreamEvent{Type: EventQueued}}

	start := false
	qe.mu.Lock()
	if kind == SubmissionSteering && qe.running {
		qe.steeringQueue = append(qe.steeringQueue, qs)
	} else {
		qe.turnQueue = append(qe.turnQueue, qs)
		if !qe.running {
			qe.running = true
			start = true
		}
	}
	qe.mu.Unlock()

	if start {
		go qe.run()
	}
	return qs.ch
}

// SubmitMessage appends a user message and runs the agent loop. Concurrent
// calls are serialized: while one loop runs, later submissions queue and are
// delivered at defined points rather than racing on shared history.
func (qe *QueryEngine) SubmitMessage(ctx context.Context, prompt string) <-chan StreamEventWithUsage {
	qe.mu.Lock()
	qe.baseCtx = ctx
	qe.mu.Unlock()
	return qe.Submit(SubmissionNewTurn, prompt)
}

// SubmitFollowUp queues prompt as the next user turn once the current loop
// finishes. If the engine is idle it runs immediately.
func (qe *QueryEngine) SubmitFollowUp(ctx context.Context, prompt string) <-chan StreamEventWithUsage {
	qe.mu.Lock()
	qe.baseCtx = ctx
	qe.mu.Unlock()
	return qe.Submit(SubmissionFollowUp, prompt)
}

// SubmitSteering queues prompt for injection at the next delivery point of
// the running loop. When idle, it starts a normal turn.
func (qe *QueryEngine) SubmitSteering(ctx context.Context, prompt string) <-chan StreamEventWithUsage {
	qe.mu.Lock()
	qe.baseCtx = ctx
	qe.mu.Unlock()
	return qe.Submit(SubmissionSteering, prompt)
}

// Cancel aborts the currently running query, if any. Partial turns are
// discarded, undelivered submissions are reported back, and the session
// stays usable for subsequent submissions.
func (qe *QueryEngine) Cancel() {
	qe.mu.Lock()
	cancel := qe.runCancel
	qe.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// run is the single runner goroutine: it owns the token until the turn
// queue drains, processing one submission at a time.
func (qe *QueryEngine) run() {
	for {
		qe.mu.Lock()
		if len(qe.turnQueue) == 0 {
			qe.running = false
			qe.runCancel = nil
			qe.mu.Unlock()
			return
		}
		qs := qe.turnQueue[0]
		qe.turnQueue = qe.turnQueue[1:]
		base := qe.baseCtx
		qe.mu.Unlock()

		runCtx, cancel := context.WithCancel(base)
		qe.mu.Lock()
		qe.runCancel = cancel
		qe.mu.Unlock()

		qe.deliverNewTurn(qs)
		qe.executeRun(runCtx, qs)

		qe.abandonSteering()
		close(qs.ch)
		cancel()
	}
}

// deliverNewTurn appends the submission's user message to the live history
// and durable store, then confirms delivery on the submission channel.
func (qe *QueryEngine) deliverNewTurn(qs queuedSubmission) {
	msg := types.FromUserText(qs.sub.Prompt)
	qe.mu.Lock()
	qe.Messages = append(qe.Messages, msg)
	qe.mu.Unlock()
	qe.persist(msg)
	sendLifecycle(qs.ch, StreamEvent{Type: EventDelivered})
}

// executeRun performs compaction, then drives RunQuery to completion,
// forwarding every event to the owning submission's channel.
func (qe *QueryEngine) executeRun(ctx context.Context, qs queuedSubmission) {
	qe.mu.Lock()
	msgs := make([]types.ConversationMessage, len(qe.Messages))
	copy(msgs, qe.Messages)

	var collapseBufferCopy []types.ConversationMessage
	if qe.collapseBuffer != nil {
		collapseBufferCopy = make([]types.ConversationMessage, len(qe.collapseBuffer))
		copy(collapseBufferCopy, qe.collapseBuffer)
	}
	qe.mu.Unlock()

	// Run compaction before the loop
	summarizeFn := qe.summarizer()

	config := qe.compactionConfig
	if config == nil {
		config = services.ResolveCompactionConfig(qe.model, 0, 0)
	}
	compactedMsgs, err := services.RunPipeline(ctx, msgs, config, &collapseBufferCopy, summarizeFn)
	if err == nil {
		msgs = compactedMsgs
		qe.mu.Lock()
		qe.collapseBuffer = collapseBufferCopy
		qe.mu.Unlock()
	}

	compCfg := qe.compactionConfig
	if compCfg == nil {
		compCfg = services.ResolveCompactionConfig(qe.model, 0, 0)
	}
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
		CompactionConfig:  compCfg,
		AskUser:           qe.askUser,
		AskPermission:     qe.askPermission,
		OnMessageAppended: qe.persist,
		DrainSteering:     qe.drainSteering,
	}

	rawCh := RunQuery(ctx, qctx, &msgs)
	for ev := range rawCh {
		if ev.Usage != nil {
			qe.costTracker.Add(ev.Usage)
		}
		qs.ch <- ev
	}

	qe.mu.Lock()
	qe.Messages = msgs
	qe.mu.Unlock()
}

// drainSteering is called by RunQuery at delivery point A. It moves every
// queued steering submission into the conversation and acknowledges delivery.
func (qe *QueryEngine) drainSteering() []types.ConversationMessage {
	qe.mu.Lock()
	subs := qe.steeringQueue
	qe.steeringQueue = nil
	qe.mu.Unlock()
	if len(subs) == 0 {
		return nil
	}

	out := make([]types.ConversationMessage, 0, len(subs))
	for _, s := range subs {
		msg := types.FromUserText(s.sub.Prompt)
		out = append(out, msg)
		qe.persist(msg)
		sendLifecycle(s.ch, StreamEvent{Type: EventDelivered})
		close(s.ch)
	}
	return out
}

// abandonSteering reports undelivered steering submissions back to their
// frontends after a run ends (only non-empty when the query was aborted).
func (qe *QueryEngine) abandonSteering() {
	qe.mu.Lock()
	subs := qe.steeringQueue
	qe.steeringQueue = nil
	qe.mu.Unlock()
	for _, s := range subs {
		sendLifecycle(s.ch, StreamEvent{Type: EventUndelivered})
		close(s.ch)
	}
}

// persist best-effort mirrors a message into the session store. Durability
// failures never abort the query; the in-memory history remains authoritative
// for the running process.
func (qe *QueryEngine) persist(msg types.ConversationMessage) {
	if qe.store == nil {
		return
	}
	if _, err := qe.store.AppendMessage(msg); err != nil {
		// Durability is best-effort; the in-memory history stays authoritative.
		return
	}
}

func sendLifecycle(ch chan StreamEventWithUsage, ev StreamEvent) {
	select {
	case ch <- StreamEventWithUsage{Event: ev}:
	default:
	}
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

// CurrentTokens returns the estimated number of tokens currently in the conversation history.
func (qe *QueryEngine) CurrentTokens() int {
	qe.mu.Lock()
	defer qe.mu.Unlock()
	return services.EstimateMessageTokens(qe.Messages)
}

// CompactionThreshold returns the token threshold at which compaction triggers.
func (qe *QueryEngine) CompactionThreshold() int {
	qe.mu.Lock()
	defer qe.mu.Unlock()
	if qe.compactionConfig != nil {
		return qe.compactionConfig.TokenThreshold
	}
	return services.ThresholdForModel(qe.model)
}

// summarizer returns the L5 summary callback: one extra LLM call that
// condenses old history into a structured summary.
func (qe *QueryEngine) summarizer() func(context.Context, string) (string, error) {
	return func(sCtx context.Context, p string) (string, error) {
		params := LLMRequestParams{
			Model:        qe.model,
			SystemPrompt: "You are a conversation summarizer.",
			Messages:     []types.ConversationMessage{types.FromUserText(p)},
			MaxTokens:    4096,
		}
		ch, err := qe.apiClient.StreamMessage(sCtx, params)
		if err != nil {
			return "", err
		}
		var summary strings.Builder
		for ev := range ch {
			if ev.Err != nil {
				return "", ev.Err
			}
			summary.WriteString(ev.TextDelta)
		}
		return summary.String(), nil
	}
}

// CompactNow runs the full compaction pipeline immediately regardless of
// threshold and swaps the result into the conversation history. It returns
// estimated tokens before and after.
func (qe *QueryEngine) CompactNow(ctx context.Context) (int, int, error) {
	qe.mu.Lock()
	msgs := make([]types.ConversationMessage, len(qe.Messages))
	copy(msgs, qe.Messages)
	var collapseBufferCopy []types.ConversationMessage
	if qe.collapseBuffer != nil {
		collapseBufferCopy = make([]types.ConversationMessage, len(qe.collapseBuffer))
		copy(collapseBufferCopy, qe.collapseBuffer)
	}
	config := qe.compactionConfig
	if config == nil {
		config = services.ResolveCompactionConfig(qe.model, 0, 0)
	}
	qe.mu.Unlock()

	before := services.EstimateMessageTokens(msgs)
	forced := *config
	forced.ForceCompact = true
	compactedMsgs, err := services.RunPipeline(ctx, msgs, &forced, &collapseBufferCopy, qe.summarizer())
	if err != nil {
		return before, before, fmt.Errorf("compact failed: %w", err)
	}

	qe.mu.Lock()
	qe.Messages = compactedMsgs
	qe.collapseBuffer = collapseBufferCopy
	qe.mu.Unlock()
	return before, services.EstimateMessageTokens(compactedMsgs), nil
}
