# Agent Note: TUI tool-line blink — defects found in review of feat_oh_0823 working tree

Status: implemented

## Problem

Code review of the uncommitted changes on `feat_oh_0823` ("tool lines re-rendered in place with a reverse-video blink on each spinner tick") found two blockers, one doc/code divergence, and several minor issues. All locations refer to `pkg/ui/tui.go` / `pkg/ui/tui_test.go` as of the working tree on 2026-08-26.

Related note (context, **not** superseded): [tui slash menu + manual compact](../../implemented/feature/2026-08-25-tui-slash-menu-manual-compact.md). Supersession check done on 2026-08-26 across `proposed/`, `implemented/`: no existing note owns these defects.

## Findings

### F1 (P1, blocker): `/clear` while a tool is running panics on the next spinner tick

- **Defect:** `dispatch("/clear")` resets `m.lines = nil` (tui.go:552–557) but does not reset `m.openTools`. The `spinner.TickMsg` handler then indexes the emptied slice:
  ```go
  for _, tc := range m.openTools {
      m.lines[tc.line] = m.renderToolLine(...)   // tui.go:192–194 → index out of range
  }
  ```
  Note `/compact` guards on `m.busy`; `/clear` has no such guard.
- **Evidence:** reproduced with a throwaway test — set `busy=true`, deliver one tool-start event, call `dispatch("/clear")`, deliver `spinner.TickMsg{}` → `panic: runtime error: index out of range [0] with length 0` at tui.go:193.
- **Fix direction:** clear `openTools` in the `/clear` branch (or reuse `finalizeAbortedTools`, tui.go:~325); additionally guard the tick loop with `tc.line < len(m.lines)` as defense in depth.

### F2 (P1, blocker): the spinner tick chain is never started — the animation is dead in production

- **Defect:** nothing in the repo ever starts the tick loop. `Init()` returns only `textinput.Blink` (tui.go:172); no caller of `m.spin.Tick` exists outside tests. Production never receives a `spinner.TickMsg`, so the new blink — and the pre-existing header/TTFT spinners — stay frozen on frame zero.
- **Aggravating detail:** even if the chain were started, the handler drops the continuation command when idle:
  ```go
  case spinner.TickMsg:
      if !m.busy {
          return m, nil   // swallows cmd → chain permanently broken after first idle tick
      }
  ```
- **Masking:** `TestSpinnerTickAnimatesRunningCards` injects a synthetic zero-value `spinner.TickMsg{}`, which hides both problems. Real messages carry `ID`/`Tag` that bubbles validates.
- **Fix direction:** `Init()` → `tea.Batch(textinput.Blink, m.spin.Tick)`; keep returning the continuation `cmd` from every `spinner.TickMsg` branch (render may be skipped when idle, but the chain must survive); drive the animation test through the real message produced by `m.spin.Tick`.

### F3 (P2): the owning Agent Note describes a different implementation than what shipped

The implemented note [2026-08-25-tui-slash-menu-manual-compact.md](../../implemented/feature/2026-08-25-tui-slash-menu-manual-compact.md) §4 claims rounded bordered cards (`╭─ icon Name ──╮`), status-in-title-icon, width-derived 28..76 truncation. The code implements a single-line reverse-video blink with fixed caps of 72/96/200 runes. Implemented notes must stay current with what shipped: either implement the card design or rewrite §4 to describe the blink. The type name `toolCardRef` is leftover vocabulary from the card design — rename to `toolLineRef`.

### F4 (minor): gofmt fails on touched files

`tui_test.go`: import order (`bubbles/spinner` before `bubbletea`). `tui.go`: struct comment alignment for `autoFollow/awaitingFirst/blink` after the `openTools` column was added. (`app.go` is also unformatted but pre-existing at HEAD.)

### F5 (minor): double truncation with inconsistent caps

`summarizeToolArgs` truncates to 72 (non-JSON path, tui.go:370) or 200 (JSON path, tui.go:387); `renderToolArgLine` then re-truncates everything to 96 (`argCap`, tui.go:340). Net effect: inconsistent effective limits plus redundant work. Truncate once, at render time.

### F6 (design opinion): full-row reverse-video flicker

Toggling `Reverse(true)+Bold` on entire lines every ~120 ms will strobe when several tools run concurrently; likely worse readability than the old `◇` marker. Consider alternating intensity instead of inversion, or animating only the icon. Non-blocking; decide before shipping.

### F7 (pre-existing, low): same-name LIFO mismatch can show wrong args

Finalization matches by tool name with LIFO fallback (tui.go:304–309). Out-of-order completion of concurrent same-name tools attributes args to the wrong row. More visible now that args persist after completion. Not introduced by this diff.

## Decision

1. **F1** — `/clear` now clears `openTools` (`tui.go: ~552`) and the
   `spinner.TickMsg` loop guards `tc.line < len(m.lines)`. Added
   `TestClearDuringToolThenTickNoPanic` (tool start → `/clear` → real tick,
   no panic, empty transcript).
2. **F2** — `Init()` now `tea.Batch(textinput.Blink, m.spin.Tick)`; the
   `spinner.TickMsg` handler always advances `m.spin` and returns the
   continuation `cmd` (even when idle), so the chain never dies.
   Animation tests now drive the real `m.spin.Tick()` message.
   Added `TestSpinnerTickChainContinues` (idle + busy ticks keep chain,
   `Init` non-nil).
3. **F3** — `toolCardRef` → `toolLineRef`; owning feature note §4 rewritten
   to describe the shipped single-line blink rendering.
4. **F4/F5** — `gofmt -w` on `pkg/ui`; single truncation at render time
   (`renderToolLine` 96 runes), `summarizeToolArgs` no longer truncates.
   F6 follow-up: full-row `Reverse+Bold` white-bar flash removed per
   user feedback ("背景不要闪") — now only the spinner glyph animates
   (no `tuiBlinkStyle` background), name/args stay stable; F7 recorded
   as known LIFO limitation.

## Alternatives considered

- Reverting the blink feature entirely: rejected — the structured
  `toolLineRef` state is a real improvement over the previous
  stripANSI-parse-and-replace approach, and the defects are localized.
- Guarding only the tick loop (defense in depth) without clearing
  `openTools` on `/clear`: insufficient — stale refs would keep "running"
  rows alive conceptually and any future index consumer would inherit the
  same trap.
- Implementing the card UI from the note instead of documenting the blink:
  larger scope than this bug-fix note; recorded as the open question in
  F3/F6 rather than bundled here.

## Consequences

- `go build ./... && go vet ./... && gofmt -l pkg/ui` is clean except for
  the pre-existing `pkg/ui/app.go` entry.
- `go test -race ./pkg/ui` passes including the new regressions above;
  existing assertions (args preserved after settle, single row per tool)
  still hold.
- `Init()` now ticks for the whole session lifetime; the handler
  short-circuits rendering when `openTools` is empty, so the cost is one
  wake-up per frame (~8 fps given the dot spinner) with no extra work.
