# AGENTS.md

openharness-go is a Go implementation of an AI coding-assistant harness: a CLI and REPL that drive a streaming LLM through a multi-turn agent loop with tool execution, context compaction, human-in-the-loop prompts, and subagents. Follow [docs/AGENTS.md](docs/AGENTS.md) for documentation rules and [.agents/notes/README.md](.agents/notes/README.md) for decision records.

## Known-state context

The codebase carries an audited debt inventory: [docs/p0-tech-debt.md](docs/p0-tech-debt.md), with adoption designs in the proposed notes under `.agents/notes/proposed/architecture/`. Its recurring failure mode is **implemented but never wired** — a subsystem complete in `pkg/` that the assembly point never connects (hooks, MCP config, and permissions all shipped disconnected). When adding or touching a subsystem:

- Wire it into a real entry point (`BuildRuntime` in pkg/ui/runtime.go or `cmd/openharness`), not just its package.
- Add at least one test that fails if the wiring is removed.

## Repository layout

```
cmd/openharness/   CLI entry (cobra): flags, mcp/auth subcommands
pkg/ui/            runtime assembly (BuildRuntime) + REPL / print / JSON-lines modes
pkg/engine/        agent loop: RunQuery turn loop + QueryEngine conversation state
pkg/api/           Anthropic and OpenAI-compatible streaming clients
pkg/tools/         BaseTool contract + ToolRegistry; builtin/ = Read, Edit, Bash, Grep, Glob, skills, tasks…
pkg/services/      context compaction pipeline
pkg/hooks/         hook executor and event bus (stream rules land here)
pkg/hitl/          human-in-the-loop adapters: CLI stdin, JSON-lines protocol manager
pkg/protocol/      frontend/backend event types for the JSON-lines protocol
pkg/tasks/         subagent registry + SubAgentExecutor
pkg/permissions/   permission modes and rule evaluation
pkg/mcp/           MCP client manager
pkg/memory/, pkg/prompts/, pkg/skills/, pkg/config/, pkg/state/, pkg/types/
docs/              documentation standard + topic notes (see docs/AGENTS.md)
.agents/           Agent Notes (notes/) and agent workflows (skills/)
design/            historical design documents
```

## Commands

```sh
go build ./...             # must pass before submitting
go vet ./...               # must pass before submitting
go test ./...              # must pass before submitting
go test -race ./...        # required when touching concurrency
go run ./cmd/openharness   # start the REPL (needs ANTHROPIC_API_KEY or configured key)
```

Match evidence to surface: focused tests for behavior changes, `-race` for goroutine/channel work, manual REPL exercise for model-visible output. CI owns nothing yet — local runs are the only gate.

## Conventions

- **Wiring completeness**: every new engine option, subsystem, or callback must be reachable from a CLI entry point; dead assembly is a review blocker ([review skill](.agents/skills/oh-code-review/SKILL.md)).
- **Provider-facing determinism**: anything sent to the LLM (tool schema arrays, system-prompt sections, skill lists) must be deterministically ordered — map iteration order silently breaks provider prompt caches.
- **Context discipline**: the session context outlives interrupts; per-query work derives its own cancellable context. A Ctrl-C must abort the current query, never brick the session.
- **Single-flight submissions**: one agent loop runs at a time; new input queues and is delivered at defined points (steering/follow-up). Never spawn a goroutine per user message.
- **Permission path end-to-end**: enforcement runs from `PermissionChecker` through `executeToolCall` to the side effect; follow denial paths when touching tool execution.
- **Bounded output**: every tool result entering conversation history carries an explicit size cap.
- **Actionable tool errors**: error results tell the model what to do next (re-read the file, add a disambiguator), not just what failed.
- **Errors wrap with `%w`**; comments state non-obvious contracts, never narrate control flow or tests.
- **Tests live inside the package under test** (`package foo_test`) and assert behavior that fails on the intended regression.

## Decisions and documentation

Every non-trivial change adds or updates at least one Agent Note in the same PR at `.agents/notes/{lifecycle}/{class}/yyyy-mm-dd-topic.md`; run the supersession check first via [oh-archive-agent-notes](.agents/skills/oh-archive-agent-notes/SKILL.md). Write and audit docs with [oh-doc-standards](.agents/skills/oh-doc-standards/SKILL.md).

## Editing these instructions

Keep each rule self-contained while linking its owning document; condense when clarity survives.
