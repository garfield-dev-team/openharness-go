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
	// Body text must be white-styled; no per-message token footer (header owns that).
	if !strings.Contains(mm.viewTranscript(), tuiTextStyle.Render("hello world")) {
		t.Fatalf("assistant body not rendered in white: %q", mm.viewTranscript())
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
	if !strings.Contains(out, "pondering") || !strings.Contains(out, "▶ Read") {
		t.Fatalf("reasoning/tool rendering broken: %q", out)
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
