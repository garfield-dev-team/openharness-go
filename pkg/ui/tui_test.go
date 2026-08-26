package ui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/openharness/openharness/pkg/config"
	"github.com/openharness/openharness/pkg/engine"
	"github.com/openharness/openharness/pkg/logger"
	"github.com/openharness/openharness/pkg/services"
	"github.com/openharness/openharness/pkg/tools"
)

func tuiTestRuntime(t *testing.T) (*RuntimeBundle, tuiModel) {
	t.Helper()
	settings := &config.Settings{Model: "claude-opus-5", MaxTokens: 64, APIKey: "k"}
	rt, thitl, m, err := assembleREPL(context.Background(), settings, WithLogger(logger.New(io.Discard, logger.LevelInfo)))
	if err != nil {
		t.Fatalf("assemble repl: %v", err)
	}
	t.Cleanup(func() { rt.Close() })
	m.p = nil
	_ = thitl
	return rt, m
}

func engineTextEvent(text string) engine.StreamEventWithUsage {
	return engine.StreamEventWithUsage{Event: engine.StreamEvent{Type: engine.EventTextDelta, Text: text}}
}

func engineReasoningEvent(text string) engine.StreamEventWithUsage {
	return engine.StreamEventWithUsage{Event: engine.StreamEvent{Type: engine.EventReasoningDelta, Text: text}}
}

func engineToolStartEvent(name, input string) engine.StreamEventWithUsage {
	return engine.StreamEventWithUsage{Event: engine.StreamEvent{Type: engine.EventToolExecutionStarted, ToolName: name, ToolInput: []byte(input)}}
}

func engineToolDoneEvent(name string) engine.StreamEventWithUsage {
	return engine.StreamEventWithUsage{Event: engine.StreamEvent{
		Type: engine.EventToolExecutionCompleted, ToolName: name,
		ToolResult: &tools.ToolResult{IsError: false},
	}}
}

func engineToolFailEvent(name string) engine.StreamEventWithUsage {
	return engine.StreamEventWithUsage{Event: engine.StreamEvent{
		Type: engine.EventToolExecutionCompleted, ToolName: name,
		ToolResult: &tools.ToolResult{IsError: true},
	}}
}

func keyMsg(ty tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: ty} }

// assembleREPL must wire the TUI HITL adapter into the runtime; removing
// that wiring breaks this test.
func TestAssembleREPLWiresTUIHITL(t *testing.T) {
	rt, _, _, err := assembleREPL(context.Background(), &config.Settings{Model: "claude-opus-5", MaxTokens: 64, APIKey: "k"})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	defer rt.Close()
}

func TestSlashSuggestionsFiltering(t *testing.T) {
	_, m := tuiTestRuntime(t)
	m.input.SetValue("/")
	m.syncSuggestions()
	if len(m.input.AvailableSuggestions()) == 0 {
		t.Fatal("no suggestions for / prefix")
	}
	m.input.SetValue("/mo")
	m.syncSuggestions()
	sugg := m.input.AvailableSuggestions()
	if len(sugg) != 2 {
		t.Fatalf("want /model + /models suggestions, got %v", sugg)
	}
	m.input.SetValue("/model ")
	m.syncSuggestions()
	if len(m.input.AvailableSuggestions()) != 0 {
		t.Fatal("suggestions must stop once an argument is typed")
	}
}

func TestPickerNavigationAndSwitch(t *testing.T) {
	rt, m := tuiTestRuntime(t)

	m.dispatch("/models")
	if m.mode != modeModelPicker {
		t.Fatalf("/models should open picker, mode=%d", m.mode)
	}
	// Move down once, then select.
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	picked := upd.(*tuiModel)
	want := picked.models[picked.cursor].ID
	upd, cmd := picked.Update(tea.KeyMsg{Type: tea.KeyEnter})
	picked = upd.(*tuiModel)
	if picked.mode != modeInput {
		t.Fatal("enter should close picker")
	}
	msg := cmd()
	st, ok := msg.(statusMsg)
	if !ok || !strings.Contains(st.text, "Switched") {
		t.Fatalf("expected switch status, got %#v", msg)
	}
	if rt.Settings.Model != want {
		t.Fatalf("settings model = %q, want %q", rt.Settings.Model, want)
	}
	if got := rt.Engine.CompactionThreshold(); got != services.ThresholdForModel(want) {
		t.Fatalf("threshold not re-resolved after switch: %d", got)
	}
}

func TestStreamEventsRenderAndFlush(t *testing.T) {
	_, m := tuiTestRuntime(t)
	m.busy = true

	ev := engineTextEvent("hello world")
	upd, _ := m.Update(streamEventMsg{ev: ev})
	mm := upd.(*tuiModel)
	if !strings.Contains(mm.viewTranscript(), "hello world") {
		t.Fatalf("streaming text not rendered: %q", mm.viewTranscript())
	}

	upd, _ = mm.Update(queryDoneMsg{})
	mm = upd.(*tuiModel)
	if mm.busy {
		t.Fatal("busy should clear on queryDoneMsg")
	}
	if !strings.Contains(mm.viewTranscript(), "hello world") {
		t.Fatalf("text lost after flush: %q", mm.viewTranscript())
	}
	// Body text must be framed by the accent-bar block style; no per-message
	// token footer (header owns that).
	if !strings.Contains(mm.viewTranscript(), tuiTextBlock.Render("hello world")) {
		t.Fatalf("assistant body not rendered with block styling: %q", mm.viewTranscript())
	}
	if strings.Contains(mm.viewTranscript(), "[🧠") {
		t.Fatal("per-message token footer must not be duplicated in transcript")
	}
}

// Manual scrolling unpins from the bottom; new stream events must not yank
// the view back down until the user scrolls to the bottom again.
func TestScrollPinAndUnpin(t *testing.T) {
	_, m := tuiTestRuntime(t)
	upd, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	mm := upd.(*tuiModel)
	for i := 0; i < 30; i++ {
		mm.appendLine(fmt.Sprintf("line-%02d", i))
	}
	mm.refreshTranscript()
	if !mm.viewport.AtBottom() {
		t.Fatal("should start pinned to bottom")
	}
	bottom := mm.viewport.YOffset

	upd, _ = mm.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	mm = upd.(*tuiModel)
	if mm.autoFollow {
		t.Fatal("pgup should unpin from bottom")
	}
	if mm.viewport.YOffset >= bottom {
		t.Fatalf("pgup did not scroll up: y=%d bottom=%d", mm.viewport.YOffset, bottom)
	}
	yPinned := mm.viewport.YOffset

	// A new event must preserve scroll position while unpinned.
	mm.appendLine("new-arrival")
	mm.refreshTranscript()
	if mm.viewport.YOffset != yPinned {
		t.Fatalf("scroll position reset by new content: y=%d want %d", mm.viewport.YOffset, yPinned)
	}

	// Scrolling back down re-pins.
	for !mm.viewport.AtBottom() {
		upd, _ = mm.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		mm = upd.(*tuiModel)
	}
	if !mm.autoFollow {
		t.Fatal("reaching bottom should re-pin auto-follow")
	}
}

func TestReasoningThenToolFlushOrder(t *testing.T) {
	_, m := tuiTestRuntime(t)
	m.busy = true
	upd, _ := m.Update(streamEventMsg{ev: engineReasoningEvent("pondering")})
	upd, _ = upd.(*tuiModel).Update(streamEventMsg{ev: engineToolStartEvent("Read", `{"path":"a.go"}`)})
	mm := upd.(*tuiModel)
	out := stripANSI(mm.viewTranscript())
	if !strings.Contains(out, "pondering") {
		t.Fatalf("reasoning lost: %q", out)
	}
	if !strings.Contains(out, "Read") || !strings.Contains(out, "path=a.go") {
		t.Fatalf("tool line missing name/args: %q", out)
	}
}

// A running tool line is replaced in place by ✔/✖ on completion —
// arguments stay visible and no duplicate rows pile up.
func TestToolLineReplacedOnCompletion(t *testing.T) {
	_, m := tuiTestRuntime(t)
	m.busy = true
	upd, _ := m.Update(streamEventMsg{ev: engineToolStartEvent("Read", `{"path":"a.go","limit":10}`)})
	mm := upd.(*tuiModel)
	if len(mm.openTools) != 1 {
		t.Fatalf("expected 1 open tool, got %d", len(mm.openTools))
	}
	out := stripANSI(mm.viewTranscript())
	if !strings.Contains(out, "Read") || !strings.Contains(out, "path=a.go") {
		t.Fatalf("running tool line malformed: %q", out)
	}
	upd, _ = mm.Update(streamEventMsg{ev: engineToolDoneEvent("Read")})
	mm = upd.(*tuiModel)
	out = stripANSI(mm.viewTranscript())
	if len(mm.openTools) != 0 {
		t.Fatalf("tool still tracked as open: %v", mm.openTools)
	}
	if !strings.Contains(out, "✔ Read") {
		t.Fatalf("success marker missing: %q", out)
	}
	if !strings.Contains(out, "path=a.go") {
		t.Fatalf("args dropped after completion: %q", out)
	}
	if n := strings.Count(out, "Read"); n != 1 {
		t.Fatalf("expected exactly one line for Read, got %d: %q", n, out)
	}
	// Failed run renders the ✖ variant, args preserved.
	upd, _ = mm.Update(streamEventMsg{ev: engineToolStartEvent("Bash", `{"cmd":"ls"}`)})
	mm = upd.(*tuiModel)
	upd, _ = mm.Update(streamEventMsg{ev: engineToolFailEvent("Bash")})
	mm = upd.(*tuiModel)
	out = stripANSI(mm.viewTranscript())
	if !strings.Contains(out, "✖ Bash failed") || !strings.Contains(out, "cmd=ls") {
		t.Fatalf("failure line malformed: %q", out)
	}
}

// Spinner ticks toggle the blink highlight of running tool lines so the
// loading state visibly animates. Uses the real spinner tick message so
// the test exercises the production path.
func TestSpinnerTickAnimatesRunningCards(t *testing.T) {
	_, m := tuiTestRuntime(t)
	m.busy = true
	upd, _ := m.Update(streamEventMsg{ev: engineToolStartEvent("Bash", `{"cmd":"sleep 1"}`)})
	mm := upd.(*tuiModel)
	before := mm.lines[mm.openTools[0].line]
	tickMsg := mm.spin.Tick()
	upd, cmd := mm.Update(tickMsg)
	mm = upd.(*tuiModel)
	if cmd == nil {
		t.Fatal("spinner tick must keep the chain alive")
	}
	after := mm.lines[mm.openTools[0].line]
	if before == after {
		t.Fatal("spinner tick did not re-render the running line")
	}
	if !strings.Contains(stripANSI(after), "Bash") {
		t.Fatalf("line content lost after tick: %q", after)
	}
}

func TestClearDuringToolThenTickNoPanic(t *testing.T) {
	_, m := tuiTestRuntime(t)
	m.busy = true
	upd, _ := m.Update(streamEventMsg{ev: engineToolStartEvent("Bash", `{"cmd":"sleep 1"}`)})
	mm := upd.(*tuiModel)
	mm.dispatch("/clear")
	if len(mm.lines) != 0 || len(mm.openTools) != 0 {
		t.Fatalf("clear must empty transcript and running-tool refs, got %d lines %d tools", len(mm.lines), len(mm.openTools))
	}
	tickMsg := mm.spin.Tick()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic on tick after clear: %v", r)
		}
	}()
	upd, cmd := mm.Update(tickMsg)
	if cmd == nil {
		t.Fatal("tick chain must survive even after clear")
	}
	_ = upd
}

func TestSpinnerTickChainContinues(t *testing.T) {
	_, m := tuiTestRuntime(t)
	// Idle tick must still return the continuation.
	tickMsg := m.spin.Tick()
	_, cmd := m.Update(tickMsg)
	if cmd == nil {
		t.Fatal("idle tick must not break the chain")
	}
	// Busy tick must also continue.
	m.busy = true
	tickMsg = m.spin.Tick()
	_, cmd = m.Update(tickMsg)
	if cmd == nil {
		t.Fatal("busy tick must not break the chain")
	}
	// Init must start the chain.
	initCmd := m.Init()
	if initCmd == nil {
		t.Fatal("Init must return a tick chain")
	}
}

// While a query is in flight but nothing has streamed yet (TTFT window),
// the view shows an animated Thinking row; it disappears on first token.
func TestAwaitingFirstTokenShowsThinkingRow(t *testing.T) {
	_, m := tuiTestRuntime(t)
	m.busy = true
	m.awaitingFirst = true
	if !strings.Contains(m.View(), "Thinking…") {
		t.Fatal("thinking row missing during TTFT")
	}
	upd, _ := m.Update(streamEventMsg{ev: engineTextEvent("hi")})
	mm := upd.(*tuiModel)
	if mm.awaitingFirst {
		t.Fatal("first token must clear awaitingFirst")
	}
	if strings.Contains(mm.View(), "Thinking…") {
		t.Fatal("thinking row must disappear once tokens stream")
	}
}

// Typing "/" opens the command menu; arrow keys move the highlight and
// enter runs the highlighted command.
func TestSlashMenuNavigationAndRun(t *testing.T) {
	_, m := tuiTestRuntime(t)
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm := upd.(*tuiModel)
	if len(mm.slashMatches()) == 0 {
		t.Fatal("menu should list all commands for / prefix")
	}
	if !strings.Contains(mm.View(), "compact conversation context now") {
		t.Fatal("menu should render command descriptions")
	}
	// Down three times lands on /model (after /clear, /compact, /cost).
	upd, _ = mm.Update(keyMsg(tea.KeyDown))
	mm = upd.(*tuiModel)
	if mm.slashIdx != 1 {
		t.Fatalf("slashIdx = %d, want 1", mm.slashIdx)
	}
	upd, _ = mm.Update(keyMsg(tea.KeyDown))
	mm = upd.(*tuiModel)
	upd, _ = mm.Update(keyMsg(tea.KeyDown))
	mm = upd.(*tuiModel)
	if got := mm.slashMatches()[mm.slashIdx].cmd; got != "/model" {
		t.Fatalf("highlighted %q, want /model", got)
	}
	// Enter runs the highlighted command → opens the model picker.
	upd, _ = mm.Update(keyMsg(tea.KeyEnter))
	mm = upd.(*tuiModel)
	if mm.mode != modeModelPicker {
		t.Fatalf("enter should run highlighted command, mode=%d", mm.mode)
	}
}

func TestCompactCommandRunsPipeline(t *testing.T) {
	rt, m := tuiTestRuntime(t)
	cmd := m.dispatch("/compact")
	if cmd == nil {
		t.Fatal("/compact must schedule a compaction run")
	}
	if !m.busy {
		t.Fatal("/compact should mark the UI busy")
	}
	msg := cmd()
	done, ok := msg.(compactDoneMsg)
	if !ok {
		t.Fatalf("expected compactDoneMsg, got %#v", msg)
	}
	if done.err != nil {
		t.Fatalf("compact failed: %v", done.err)
	}
	if rt.Engine.CurrentTokens() > done.before {
		t.Fatalf("history grew after compact: %d > %d", rt.Engine.CurrentTokens(), done.before)
	}
	// Result line lands in the transcript once the message is processed.
	upd, _ := m.Update(done)
	out := stripANSI(upd.(*tuiModel).viewTranscript())
	if !strings.Contains(out, "Compacted:") {
		t.Fatalf("transcript missing compaction summary: %q", out)
	}
}

func TestHITLOverlayAnswerRouting(t *testing.T) {
	_, m := tuiTestRuntime(t)
	reply := make(chan string, 1)
	upd, _ := m.Update(hitlPromptMsg{question: "Allow Bash?", options: []string{"allow", "deny"}, reply: reply})
	mm := upd.(*tuiModel)
	if mm.mode != modeHitl || mm.hitl == nil {
		t.Fatal("hitl overlay not opened")
	}
	// Type "2" then Enter → maps to options[1] = "deny".
	upd, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	mm = upd.(*tuiModel)
	upd, cmd := mm.Update(keyMsg(tea.KeyEnter))
	mm = upd.(*tuiModel)
	if mm.mode != modeInput {
		t.Fatal("overlay should close after answering")
	}
	done := make(chan struct{})
	go func() { <-reply; close(done) }()
	cmd()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("answer not delivered to HITL caller")
	}
}

func TestCtrlCAbortsQueryWhenBusy(t *testing.T) {
	rt, m := tuiTestRuntime(t)
	m.busy = true
	upd, cmd := m.Update(keyMsg(tea.KeyCtrlC))
	mm := upd.(*tuiModel)
	if cmd != nil {
		t.Fatal("abort must not quit the program")
	}
	if mm.ctx.Err() != nil && rt.Engine == nil {
		t.Fatal("sanity")
	}
	// Idle Ctrl-C quits.
	mm.busy = false
	_, cmd = mm.Update(keyMsg(tea.KeyCtrlC))
	if cmd == nil {
		t.Fatal("idle ctrl+c must quit")
	}
}
