# Agent Note: Token Budget context management (Codex experimental_mode alignment)

Status: proposed

## Problem

The five-stage compaction pipeline (`pkg/services/compact.go`) is lossy by design: L5 replaces history with an LLM summary, and everything the summary drops is gone. The model cannot see how much context remains, cannot plan around the window, and cannot recover detail after compaction. OpenAI Codex shipped an alternative ("token budget" behind `features.context_management.experimental_mode`): instead of summarizing, roll over to a fresh context window, keep full history queryable through history/notes tools, and make the budget visible to the model. Research: [codex-context-management-research.md](../../../../docs/codex-context-management-research.md).

## Proposal

Add a token-budget context-management path alongside (not replacing) the compaction pipeline, gated by two settings (`context_management` master switch, `token_budget` mechanism config). Core pieces:

- `pkg/services/tokenbudget`: window state machine (UUIDv7 id triplet, one-shot reminder/fallback claims, `new_context` request flag, server-observed prefill baseline) and accounting (BodyAfterPrefix scope, dual limit with fallback buffer) — a direct port of Codex `AutoCompactWindow` + `ContextWindowTokenStatus`.
- Perception: injected `role: user` system-synthesized messages carrying `<context_window>` metadata, guidance (diff-on-change), one-shot threshold reminder, and a zero-remaining fallback prompt backed by a reserved buffer.
- Management: `new_context` and `get_context_remaining` tools (registered only when the budget mode is active); rollover replaces history with fresh initial context, writes the first-ever `KindCompaction` session entry, and fires new `PreCompact`/`PostCompact` hooks plus a `context_window_reset` event so all three trigger paths (tool request, token limit, manual /compact) are indistinguishable to hooks and UI.
- Memory: local-first `notes.*` (files under the session directory, per-agent prefix isolation) and `history.*` (reads live history + session JSONL, so rolled-over windows stay queryable) tools, nine total, every output hard-bounded.
- Wiring: `BuildRuntime` constructs the bridge and registers the tools only when activated; `SwitchModel` re-resolves the budget; a wiring test fails if the engine runs without the bridge while the feature is on.

Prerequisite fix: the Anthropic client drops `message_start`, so `UsageSnapshot.InputTokens` is always 0; the prefill baseline needs it parsed.

## Alternatives considered

- **Keep only the compaction pipeline, tune thresholds.** Loses the model-side agency that makes the Codex mechanism work: without visible budget and a self-serve reset, any window policy stays reactive. Rejected as the primary path; the pipeline remains the default when the feature is off.
- **Implement rollover inside the compaction pipeline as an "L6".** Overloads a lossy-summarization abstraction with a lossless mechanism and couples their configs; Codex deliberately keeps rollover a separate mode entered via feature flags. Rejected.
- **Server-backed history/notes like Codex's `alpha/*` endpoints.** We have no backend to host it; local files and the existing session JSONL already satisfy "switch window without amnesia". Rejected; encryption/truncation headers are dropped with it.
- **Model-owned defaults auto-activation (Codex `ModelMessages.token_budget`).** Our model registry is a static table without per-model prompt payloads; auto-activation without that payload would silently change sessions. Deferred until the table grows model-specific budget fields.

## Acceptance criteria

- With `context_management` + `token_budget.enabled` off, engine behavior is byte-identical to today (compaction pipeline untouched, no new tools registered).
- With it on: a scripted model that calls `new_context` gets a fresh window on the next request (old messages absent, `<context_window>` metadata present), a `KindCompaction` entry is persisted, and reminder/fallback messages each appear at most once per window.
- History tools list and read items from windows discarded by an earlier rollover after a session resume.
- Every notes/history tool output respects its documented bound; oversized `notes.write_file` text is rejected with an actionable error.
- `go test -race ./pkg/engine/... ./pkg/services/tokenbudget/...` passes; BuildRuntime wiring test fails when the bridge is removed while activated.

## Risks

- Token accounting still leans on estimates when the first response of a window has no usage data; drift could delay rollover until the hard window limit. Mitigation: dual limit (scope + full window) and the P0 usage fix.
- Injected budget metadata consumes real context each window; the 2000-byte template caps bound worst-case overhead.
- Subagent notes isolation depends on threading an agent path into `ToolExecutionContext`; missing it would let subagents write into root notes (the exact defect Codex review caught in PR #39827).
