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

4. **Tool lines update in place**: `◇ Name key=value…` (orange name,
   dim sorted args) while running; the same transcript row flips to
   `✔ Name` (green) / `✖ Name failed` (red) on completion via
   `openTools` index tracking (matched by tool name, LIFO fallback).
   Abort finalizes dangling rows as `⏹ Name interrupted`. Args render as
   alphabetically sorted JSON key=value pairs capped at 72 runes —
   deterministic and short.

5. **TTFT indicator**: between submit and first streamed token,
   `awaitingFirst` renders an animated spinner + "Thinking…" row under
   the viewport; any Text/Reasoning/Tool event clears it.

## Wiring

`/compact` is reachable from the REPL input (slashCommands + dispatch);
covered by TestCompactCommandRunsPipeline (fails if dispatch loses the
case). Menu behavior covered by TestSlashMenuNavigationAndRun. In-place
tool row replacement by TestToolLineReplacedOnCompletion; TTFT row by
TestAwaitingFirstTokenShowsThinkingRow.
