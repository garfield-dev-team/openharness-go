# TUI: slash command menu, accent-bar body style, manual /compact

Date: 2026-08-25
Status: implemented
Scope: pkg/ui/tui.go, pkg/engine/query_engine.go, pkg/services/compact.go

## Decisions

1. **Assistant body differentiation**: prose blocks render inside
   `tuiTextBlock` — bright-white text framed by a cyan left border bar
   (`│ `), visually distinct from dim reasoning and orange tool lines.
   Chosen over a markdown renderer (glamour) to avoid re-render hazards on
   partial stream chunks; revisit if users ask for rich markdown.

2. **Slash menu**: typing `/` opens an in-view menu (above the input) of
   prefix-matched commands with descriptions. While the menu is open,
   ↑/↓ move the highlight (they do NOT scroll the transcript), Tab
   completes into the input, Enter runs the highlighted command, Esc
   dismisses. The old textinput ghost-suggestions remain as a secondary
   affordance. Gotcha fixed during testing: reset `slashIdx` *after*
   capturing the picked index, or Enter always dispatches row 0.

3. **Manual /compact**: `CompactionConfig.ForceCompact` (new) skips the
   `ShouldCompact` gates inside `RunPipeline` so L5 runs even below
   threshold. New `QueryEngine.CompactNow(ctx)` snapshots messages +
   collapse buffer under lock, runs the pipeline with a force-enabled
   config copy (never mutates the shared config), swaps results back, and
   returns before/after token estimates. UI marks busy, streams
   `compactDoneMsg`, prints `✔ Compacted: X → Y tokens`. Refused while a
   turn is in flight (single-flight rule).

4. **Tool lines (single-line, Codex-style icon pulse)**: each tool call
   renders as one line `icon Name args…` — icon is the spinner frame
   while running, `✔` green / `✖ failed` red / `⏹ interrupted` yellow
   when settled. `openTools []toolLineRef` tracks running lines; spinner
   ticks toggle `blink` and re-render in place, and completion/abort
   flips the same rows (no duplicate blocks, args preserved). The
   running animation is Codex-style: only the leading spinner icon
   pulses (alternating `tuiToolStyle` / `tuiBlinkStyle` per tick), the
   name (`tuiToolStyle`) and args (`tuiDimStyle`) stay stable — no
   full-row white bar. Args render as alphabetically sorted `key=value`
   pairs, newlines shown as `⏎`, truncated once at render time to 96
   runes. F7: same-name LIFO mismatch remains — concurrent same-name
   tools completing out of order may attribute args to the wrong row;
   not fixed here.

5. **TTFT indicator**: between submit and first streamed token,
   `awaitingFirst` renders an animated spinner + "Thinking…" row under
   the viewport; any Text/Reasoning/Tool event clears it.

## Wiring

`/compact` is reachable from the REPL input (slashCommands + dispatch);
covered by TestCompactCommandRunsPipeline (fails if dispatch loses the
case). Menu behavior covered by TestSlashMenuNavigationAndRun. Tool-line
rendering, in-place status flip, and spinner-driven blink by
TestToolLineReplacedOnCompletion and TestSpinnerTickAnimatesRunningCards;
`/clear`-while-running guard by TestClearDuringToolThenTickNoPanic;
tick-chain liveness by TestSpinnerTickChainContinues; TTFT row by
TestAwaitingFirstTokenShowsThinkingRow.
