package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/openharness/openharness/pkg/engine"
	"github.com/openharness/openharness/pkg/tools"
	"github.com/openharness/openharness/pkg/types"
)

// ---------------------------------------------------------------------------
// Test doubles.
// ---------------------------------------------------------------------------

// scriptedClient plays a queue of scripted assistant responses, one per
// StreamMessage call, and records the message history of every request.
type scriptedClient struct {
	mu       sync.Mutex
	scripts  []scriptedTurn
	calls    int
	requests [][]types.ConversationMessage

	started   chan struct{} // immutable after construction; closed once
	startOnce sync.Once
}

// scriptedTurn is either a plain text reply or a single tool_use request.
type scriptedTurn struct {
	text    string
	tool    string
	toolArg map[string]any
}

func (c *scriptedClient) push(s scriptedTurn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scripts = append(c.scripts, s)
}

func (c *scriptedClient) next() (scriptedTurn, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls >= len(c.scripts) {
		return scriptedTurn{}, false
	}
	s := c.scripts[c.calls]
	c.calls++
	return s, true
}

func (c *scriptedClient) record(msgs []types.ConversationMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]types.ConversationMessage, len(msgs))
	copy(cp, msgs)
	c.requests = append(c.requests, cp)
	if c.started != nil {
		c.startOnce.Do(func() { close(c.started) })
	}
}

func (c *scriptedClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *scriptedClient) requestSnapshots() [][]types.ConversationMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]types.ConversationMessage, len(c.requests))
	copy(out, c.requests)
	return out
}

func (c *scriptedClient) StreamMessage(ctx context.Context, params engine.LLMRequestParams) (<-chan engine.LLMStreamEvent, error) {
	c.record(params.Messages)
	turn, ok := c.next()
	ch := make(chan engine.LLMStreamEvent, 4)
	if !ok {
		close(ch)
		return ch, nil
	}
	msg := types.ConversationMessage{Role: "assistant"}
	if turn.tool != "" {
		msg.Content = append(msg.Content, types.ContentBlock{
			Type: "tool_use", ID: fmt.Sprintf("toolu_%d", time.Now().UnixNano()), Name: turn.tool, Input: turn.toolArg,
		})
	} else {
		msg.Content = append(msg.Content, types.NewTextBlock(turn.text))
	}
	go func() {
		defer close(ch)
		ch <- engine.LLMStreamEvent{TextDelta: turn.text}
		ch <- engine.LLMStreamEvent{Message: &msg, Usage: &types.UsageSnapshot{InputTokens: 1, OutputTokens: 1}}
	}()
	return ch, nil
}

// blockingTool signals it started, then blocks until released — lets tests
// inject steering while the loop is parked inside tool execution.
type blockingTool struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingTool) Name() string        { return "block" }
func (b *blockingTool) Description() string { return "blocks until released" }
func (b *blockingTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (b *blockingTool) IsReadOnly(json.RawMessage) bool { return true }
func (b *blockingTool) ToAPISchema() map[string]any {
	return map[string]any{"name": b.Name(), "input_schema": b.InputSchema()}
}
func (b *blockingTool) Execute(_ context.Context, _ json.RawMessage, _ *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	close(b.started)
	<-b.release
	return &tools.ToolResult{Output: "done"}, nil
}

// drain consumes a submission channel to completion and returns its events.
func drain(ch <-chan engine.StreamEventWithUsage) []engine.StreamEventWithUsage {
	var out []engine.StreamEventWithUsage
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func hasEvent(evs []engine.StreamEventWithUsage, t engine.EventType) bool {
	for _, ev := range evs {
		if ev.Event.Type == t {
			return true
		}
	}
	return false
}

// hasToolResult reports whether a message carries any tool_result block.
func hasToolResult(m types.ConversationMessage) bool {
	for _, b := range m.Content {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Tests.
// ---------------------------------------------------------------------------

func newTestEngine(t *testing.T, client engine.StreamingLLMClient) *engine.QueryEngine {
	t.Helper()
	reg := tools.NewToolRegistry()
	return engine.NewQueryEngine(client, reg, t.TempDir(), "test-model", "sys", 1024,
		engine.WithMaxTurns(8))
}

// Submit called directly (JSONLines path) on a fresh engine must run even
// though no SubmitMessage ever provided a context — nil baseCtx would panic.
func TestSubmitDirectlyWithoutSubmitMessage(t *testing.T) {
	client := &scriptedClient{}
	client.push(scriptedTurn{text: "direct"})
	qe := newTestEngine(t, client)

	evs := drain(qe.Submit(engine.SubmissionNewTurn, "hello"))
	if hasEvent(evs, engine.EventError) {
		t.Fatalf("direct Submit errored: %+v", evs)
	}
	if client.callCount() != 1 {
		t.Fatalf("expected one LLM call, got %d", client.callCount())
	}
}

// Concurrent submissions must serialize: exactly one LLM call per submission,
// each seeing strictly growing history in submission order.
func TestConcurrentSubmitsSerializeSingleFlight(t *testing.T) {
	client := &scriptedClient{}
	for i := 0; i < 6; i++ {
		client.push(scriptedTurn{text: fmt.Sprintf("reply %d", i)})
	}
	qe := newTestEngine(t, client)

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			drain(qe.SubmitMessage(context.Background(), fmt.Sprintf("msg %d", i)))
		}(i)
	}
	wg.Wait()

	if got := client.callCount(); got != 6 {
		t.Fatalf("expected 6 serialized LLM calls, got %d", got)
	}
	snaps := client.requestSnapshots()
	for i, req := range snaps {
		// Request i must carry exactly i prior user turns + its own prompt.
		userMsgs := 0
		for _, m := range req {
			if m.Role == "user" && m.GetText() != "" {
				userMsgs++
			}
		}
		if userMsgs != i+1 {
			t.Fatalf("request %d saw %d user messages, want %d", i, userMsgs, i+1)
		}
	}
}

// Steering submitted during tool execution is injected at delivery point A:
// after the tool-result message and before the next LLM call.
func TestSteeringInjectedAtTurnBoundary(t *testing.T) {
	client := &scriptedClient{started: make(chan struct{})}
	client.push(scriptedTurn{tool: "block"})
	client.push(scriptedTurn{text: "final"})

	block := &blockingTool{started: make(chan struct{}), release: make(chan struct{})}
	reg := tools.NewToolRegistry()
	reg.Register(block)
	qe := engine.NewQueryEngine(client, reg, t.TempDir(), "m", "sys", 1024, engine.WithMaxTurns(8))

	runCh := qe.SubmitMessage(context.Background(), "start")
	started := client.started
	<-started // first LLM call under way

	runDone := make(chan struct{})
	go func() {
		for range runCh { //nolint:revive — events not inspected here
		}
		close(runDone)
	}()

	// Wait until tool execution began, then submit steering mid-run.
	select {
	case <-block.started:
	case <-time.After(2 * time.Second):
		t.Fatal("tool never started")
	}
	steerCh := qe.SubmitSteering(context.Background(), "steer me")

	// Delivery happens at the turn boundary after the tool result; unblock
	// the tool, confirm steering was delivered, and let the run finish.
	close(block.release)
	steerEvents := drain(steerCh)
	if !hasEvent(steerEvents, engine.EventDelivered) {
		t.Fatalf("steering never delivered: %+v", steerEvents)
	}
	<-runDone

	// The second LLM call must contain: start → assistant(tool_use) →
	// tool result → steering, in that order.
	snaps := client.requestSnapshots()
	if len(snaps) < 2 {
		t.Fatalf("expected 2 requests, got %d", len(snaps))
	}
	req := snaps[1]
	var order []string
	for _, m := range req {
		switch {
		case m.Role == "user" && hasToolResult(m):
			order = append(order, "tool_result")
		case m.Role == "assistant":
			order = append(order, "assistant")
		case m.Role == "user":
			order = append(order, m.GetText())
		}
	}
	want := []string{"start", "assistant", "tool_result", "steer me"}
	if len(order) != len(want) {
		t.Fatalf("delivery point violated: %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("delivery point violated:\n got %v\nwant %v", order, want)
		}
	}
}

// A follow-up submitted while a run executes starts its own run afterwards.
func TestFollowUpRunsAfterCurrentLoopCompletes(t *testing.T) {
	client := &scriptedClient{}
	client.push(scriptedTurn{text: "first done"})
	client.push(scriptedTurn{text: "second done"})
	qe := newTestEngine(t, client)

	runCh := qe.SubmitMessage(context.Background(), "one")
	followCh := qe.SubmitFollowUp(context.Background(), "two")
	drain(runCh)
	drain(followCh)

	if got := client.callCount(); got != 2 {
		t.Fatalf("expected follow-up to trigger second run, calls=%d", got)
	}
	snaps := client.requestSnapshots()
	last := snaps[len(snaps)-1]
	found := false
	for _, m := range last {
		if m.Role == "user" && m.GetText() == "two" {
			found = true
		}
	}
	if !found {
		t.Fatal("follow-up prompt missing from final request")
	}
}

// Cancel aborts the running query; queued steering comes back undelivered
// and the engine accepts new work afterwards.
func TestCancelAbortsRunAndReturnsUndeliveredSteering(t *testing.T) {
	client := &scriptedClient{started: make(chan struct{})}
	client.push(scriptedTurn{tool: "block"})

	block := &blockingTool{started: make(chan struct{}), release: make(chan struct{})}
	reg := tools.NewToolRegistry()
	reg.Register(block)
	qe := engine.NewQueryEngine(client, reg, t.TempDir(), "m", "sys", 1024, engine.WithMaxTurns(8))

	runCh := qe.SubmitMessage(context.Background(), "start")
	started := client.started
	<-started

	aborted := make(chan struct{})
	go func() {
		for ev := range runCh {
			if ev.Event.Type == engine.EventAborted {
				close(aborted)
			}
		}
	}()

	// Queue steering while parked inside the blocked tool, then cancel.
	<-block.started
	steerCh := qe.SubmitSteering(context.Background(), "too late")
	qe.Cancel()
	close(block.release)

	select {
	case <-aborted:
	case <-time.After(2 * time.Second):
		t.Fatal("query did not report abort")
	}

	// The queued steering must come back undelivered, never injected.
	evs := drain(steerCh)
	if hasEvent(evs, engine.EventDelivered) {
		t.Fatal("stale steering must not be delivered after abort")
	}
	if !hasEvent(evs, engine.EventUndelivered) {
		t.Fatalf("expected undelivered receipt: %+v", evs)
	}

	// The session must remain usable.
	client.push(scriptedTurn{text: "recovered"})
	drain(qe.SubmitMessage(context.Background(), "again"))
	if client.callCount() != 2 {
		t.Fatalf("engine unusable after abort, calls=%d", client.callCount())
	}
}
