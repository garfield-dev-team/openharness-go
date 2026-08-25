# Agent Note: REPL rewritten as Bubble Tea TUI

Status: implemented

## Decision

The interactive mode (`ui.RunREPL`) is a Bubble Tea (Elm-architecture) program, replacing the line-based `bufio.Scanner` REPL. All rendering goes through Bubble Tea's frame diffing — no raw `fmt.Print` to the terminal from the REPL path.

- **Package layout**: `pkg/ui/tui.go` (model/update/view), `pkg/ui/tui_hitl.go` (HITL adapter), `pkg/ui/tui_test.go` (internal tests). `RunREPL` in `app.go` only assembles (`assembleREPL`) and runs the program. Print/JSON modes and the JSONLines protocol mode are unchanged.
- **Event flow**: the agent loop runs on a goroutine; every `engine.StreamEventWithUsage` is forwarded via `program.Send(streamEventMsg{...})` and applied inside `Update`, so all UI state mutation stays on the UI goroutine. Query completion sends `queryDoneMsg`.
- **Input**: `bubbles/textinput` with slash-command suggestions (`syncSuggestions` filters `slashCommands` while input is `/…` with no space; Tab/→ accepts). This fixes both missing autocomplete and the backspace residue of manual printing.
- **Model switching**: `/model` or `/models` opens an inline picker (`↑/↓`, Enter, Esc); `/model <id>` switches directly. Both route through `RuntimeBundle.SwitchModel`. The picker list order is `services.ListKnownModels()` (deterministic).
- **Single-flight preserved**: submitting while busy queues the line as `engine.SubmissionSteering` instead of dropping it.
- **Ctrl-C two-stage** (same contract as before): aborts the running query when busy, exits when idle.
- **HITL**: `tuiHITL` implements the same `AskUser`/`AskPermission` callbacks as `hitl.CLIAdapter` but resolves them through a TUI overlay (`hitlPromptMsg` + reply channel), so prompts never corrupt the frame.
- **Dependencies added**: charmbracelet bubbletea, bubbles, lipgloss (+ transitive). Pure Go, no CGO.

## Rejected alternatives

- **tview/tcell**: full-screen widget framework, heavier than needed for one transcript+input surface.
- **promptui/survey**: would patch pickers but keep the broken hand-rendered input loop.
- **Keeping HandleLine for interactive use**: it renders by writing ANSI to a logger mid-stream, which cannot compose with frame-diffed TUI output; the TUI consumes engine events directly (same pattern as `RunJSONLinesMode`).

## Consequences

- `HandleLine` remains for print mode and tests but is no longer the interactive render path; regressions here are caught by `pkg/ui/tui_test.go` (picker switch re-resolves compaction threshold; `TestAssembleREPLWiresTUIHITL` fails if HITL wiring is removed).
- Bubble Tea takes over SIGINT handling; there is no separate `signal.Notify` in the REPL anymore.
- Tests construct the unexported model directly (`package ui` internal test) because Elm-style state is only reachable without a real TTY.
