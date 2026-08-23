---
name: oh-code-review
description: Use when reviewing a pull request in openharness-go — orients the reviewer to this codebase's standards (AGENTS.md conventions, Go concurrency and security patterns, Agent Notes, quality gates) and the review-specific checks that code alone can't show.
---

# Reviewing an openharness-go PR

**This skill is guidance, not a complete checklist.** Verify and fetch the PR's live base and exact head before reading the diff and enough surrounding code to understand the design. Prioritize correctness, lifecycle, security, and broken required behavior over style; a short review with one substantiated blocker is better than a list of nits.

## Sources of truth

- Root [AGENTS.md](../../../AGENTS.md): standing repository rules — build/vet/test must pass before submission.
- [docs/AGENTS.md](../../../docs/AGENTS.md): documentation placement and prose discipline.
- [Agent Notes](../../notes/README.md): design rationale. Treat disagreement with an Agent Note as a design discussion, not an automatic veto.
- [docs/p0-tech-debt.md](../../../docs/p0-tech-debt.md) and its linked research notes: known defect inventory — check whether a PR touches or re-introduces one of the audited "wired but dead" patterns (subsystems built in `pkg/` but never assembled in `pkg/ui/runtime.go`).

## Blocking requirements

1. **Every non-trivial change carries an Agent Note in the same diff** — updated owner note or a new one under `.agents/notes/{lifecycle}/{class}/`, with the supersession check done. A `proposed/` note implemented by this PR is moved to `implemented/` and rewritten in present tense in the same diff; verify its paths, names, and mechanisms against the implementation.
2. **Docs match the code.** Config fields, defaults, error strings, wire formats, and public behavior update the owning README/doc in the same diff. Comments state non-obvious contracts; flag implementation narration, test walkthroughs, review history, and duplicated rationale for deletion or a link to their one home.
3. **Wiring completeness.** This repo has a recurring failure mode: a subsystem is fully implemented but never connected at the assembly point (`BuildRuntime` in pkg/ui/runtime.go shipped hooks, MCP config, and permissions all disconnected). For every new package or option wired into the engine, verify it is reachable from an actual CLI entry point and covered by at least one test that would fail if the wiring were removed.
4. **Concurrency safety.** Run `go test -race ./...` on the touched packages. Any goroutine spawned per request/message must have a stated serialization or ownership story (see the single-flight queue design in docs/pi-omp-implementation-design.md §0); shared stdout writes, unsynchronized map iteration feeding ordered output, and fire-and-forget goroutines capturing session-wide context are blockers.
5. **Required evidence exists.** Verify the author ran `go build ./...`, `go vet ./...`, `go test ./...`; review the semantic gaps neither can detect.

## Manual checks

- **Intent and interface contracts:** trace both sides of every changed interface. Confirm the implementation matches the PR and any Agent Note, including errors, cancellation, and cleanup (`defer rt.Close()` paths).
- **Lifecycle and concurrency:** for goroutines, channels, contexts, and teardown — check races before publication, cancellation propagation (a per-query derived ctx vs. the session ctx), channel deadlock on early return (sender blocked on an unbuffered channel nobody drains), and `sync.WaitGroup` pairing. Contexts must be cancelable without bricking long-lived sessions.
- **Scope, ownership, and necessity:** map each abstraction and state machine to its current contract and production consumer. Challenge unrelated features and speculative generality; a new public method whose only caller is internal should be private.
- **Security and permission enforcement:** changes touching tool execution must keep the permission path enforced end-to-end — from `PermissionChecker` through `executeToolCall` to the actual side effect. Follow every denial path to the operation that executes it; exercise direct callers that could bypass prompts or facades.
- **Model perspective:** inspect the exact prompts, tool schemas, results, and diagnostics the model receives across affected modes (REPL, print, JSON lines). Flag nondeterministic ordering in anything fed to the provider (tool schema arrays must be sorted) — it silently breaks prompt caching.
- **Test strength:** assertions fail on the intended regression and verify external state rather than restating the implementation. Coverage is necessary but not evidence that the scenario is correct.
- **Bounded output:** tool results flowing into conversation history need explicit size bounds; probe tiny limits, oversized single chunks, and multibyte text.

## Reporting findings

State the defect, location, impact, and evidence. Place a localized defect inline on the tightest relevant diff range; use a PR-level comment for cross-cutting architecture, scope, or review-wide synthesis. Separate blockers from suggestions and omit issues already enforced by a green gate. When receiving review, verify each claim and fix or rebut it on technical grounds without performative agreement.
