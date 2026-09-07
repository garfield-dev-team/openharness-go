# Agent Note: Harness V2 durability core adoption

Status: proposed

## Problem

The engine's durability contract is best-effort: message persistence swallows errors with in-memory history authoritative (`QueryEngine.persist`), compaction never reaches the session tree (`KindCompaction` has no writer), provider-stream interruption terminates the query with no step or attempt concept (`RunQuery`), tool execution records no intent before its effect, and `Cancel` discards partial turns leaving no trace in the session tree. A crash or Ctrl-C mid-run loses the run, its attempt counts, its tool outcomes, and its spend. Pi's harness-v2 design shows the target semantics for all of these; the mechanism research lives in [docs/harness-v2-research.md](../../../../docs/harness-v2-research.md).

Scope note: compaction mechanics (cut points, structured summaries, incremental semantics) are already owned by [the pi/omp implementation design note](2026-08-22-pi-omp-implementation-design.md) item #10; this note does not re-propose them. This note covers what that note does not own: making an accepted prompt a durable operation.

## Proposal

Adopt the V2 durability core in three phases, each independently shippable and each wired through `BuildRuntime` with a test that fails if the wiring is removed.

**Phase 1 — compaction lands in the tree + append-only context invariant.** Persist every compaction as a `KindCompaction` entry (summary, retained tail, tokens-before); context building stops at the newest compaction entry. Add the Tier-B-style executable invariant: within a run, each provider request's message list extends the previous request's as an exact prefix except across a compaction entry. Re-point the oversized-tool-result truncation at prune+spill (spilled content keeps a retrievable path) instead of character truncation.

**Phase 2 — minimal operation records.** Add three record kinds to the session store (`operation_started`, `step_attempt`, `tool_started`, plus `operation_finished`) with provisioned entry ids and a monotonic per-session sequence. Attempt counts become durable: stream interruption leaves an unfinished step that recovery retries under the cap instead of dying; the transport retry in `pkg/api` feeds the engine-visible attempt count. Tools declare `Replay() ("never" | "safe")`; recovery re-executes an intent-without-result only when both the record and the current declaration say safe, otherwise writes a synthetic "interrupted" result. Abort becomes reconciliation (synthetic tool results, closing assistant message, `operation_finished aborted`) instead of discarding. Every provider request settles a usage record before any retry or discard decision.

**Phase 3 — lanes and the observation surface.** Named leaves beyond `main` with per-lane queues and per-lane configuration entries; subagents may run on a second lane of the parent session. Snapshot-then-events protocol for `pkg/protocol` frontends. Hook surface expansion (`transform_context`, `before_request`, `after_response`, `before_compaction`) with fail-closed `before_tool`. An `Effects`-style boundary in the engine enabling a gated manual-drive mode for deterministic crash-site tests (Tier A/B/C adapted to `go test`).

## Alternatives considered

**Keep best-effort persistence and harden ad hoc.** Cheapest, but every fix is local: attempt counts stay in-memory, tool outcomes stay undefined after cancellation, and cost stays unauditable. The 50-hour-run semantics the framework will need are exactly the ones ad hoc hardening cannot reach.

**Full V2 port at once** (SQLite backend, writer leases, deferred provider requests, the Result/TaggedError API shape, TypeBox validation). Loses on fit: single-process CLI has no multi-writer problem, Go idiom has no equivalent of exhaustive `matchError`, and deferred requests depend on narrow provider support. The JSONL store already matches V2's own JSONL backend shape (one line = one atomic mutation); the port would buy surface area, not semantics.

**Only phase 1.** Fixes the compaction hole and locks the cache invariant but leaves crash-mid-tool and mid-stream undefined — the majority of the durability win sits in phase 2.

## Acceptance criteria

- A run interrupted by kill -9 restores on next start: accepted input is present, an unfinished step resumes or closes with a durable outcome, unresolved tool calls resolve per replay policy, and usage spent before the crash is recorded.
- Compaction produces a tree entry; reopening a compacted session rebuilds context from the compaction entry without re-summarizing.
- The append-only-context test fails if any write path inserts before the tail of an in-flight run's context.
- `go test ./...` passes with `-race` for the engine changes; recovery is idempotent (re-running recovery after a crash mid-recovery is safe).

## Risks

The session store gains record lines interleaved with entries — every consumer of the JSONL (list, fork, resume path) must skip or reduce records; fork semantics follow V2 (records are not copied, forks start idle). Attempt-based retry changes visible behavior on provider errors: queries that previously died now retry and bill, which needs a default cap and an escape hatch. Phase 3's lane surface touches `pkg/protocol` and every frontend adapter and should not start before phase 2 has shipped in production use.
