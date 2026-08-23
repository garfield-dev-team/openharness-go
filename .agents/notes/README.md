# Agent Notes

One kind of design doc lives here: an **Agent Note** records a decision or proposal that affects this codebase — the *why* and *what we gave up*, the parts code and ordinary docs cannot carry. This file defines where Agent Notes live, when to write one, and [the in-file format](#the-file-format). The convention is modeled on [deepseek-harness/.agents/notes](https://github.com/deepseek-ai/deepseek-harness/blob/master/.agents/notes/README.md).

## Layout and naming

Every Agent Note has two axes, both encoded in its **path**: `{lifecycle}/{class}/yyyy-mm-dd-topic-title.md`.

- **Lifecycle** (the top-level folder) is the status; a note moves between folders as its status changes:
  - **`proposed/`** — proposals reviewed before implementation; not yet built (or only partly).
  - **`implemented/`** — the decision shipped. The file records what was decided and what was rejected, and is **kept current with what actually shipped**: when later code moves files, renames packages, or changes key defaults, the same change updates the note's factual content (paths, names, structure) — never the decision itself.
  - **`rejected/`** — the proposal was considered and declined. Keep it while its rationale prevents a tempting, meaningful mistake; otherwise delete it together with any related notes.
- **Class** (the nested folder) is the kind of decision — see [Classification](#classification).

The date in the filename is when the topic was **first proposed** (per git history). Cross-references between notes use relative markdown links (`[topic](../../implemented/architecture/2026-…-….md)`), never bare prose or numbers — so links are mechanically checkable and survive moves between folders. Do not build a centralized INDEX.md; browse the lifecycle/class folders or search the repository.

## Classification

| Class | What it covers |
|---|---|
| `feature` | A new user-facing or model-facing capability. |
| `bug-fix` | Corrects a defect, or closes a gap an audit or postmortem surfaced. |
| `simplification` | Removes code, behavior, or surface area without adding a capability. |
| `architecture` | A structural decision about the **shipped source** — how packages relate, what the runtime vocabulary is. |
| `process` | Tooling, policy, or workflow **around** the code — gates, dependency management, scripts — not runtime behavior. |
| `testing` | Test infrastructure and strategy. |

The `architecture` / `process` line: **architecture** is about the source we ship; **process** is about the surrounding tooling and workflow. (`refactor` is deliberately absent — it overlaps `simplification`, whose discriminator "does observable behavior change?" already covers it.)

## When to write one

Every non-trivial change adds or updates at least one Agent Note in the same PR. Non-trivial means: changes behavior, architecture, a contract shared across files or packages, process or tooling, testing strategy, on-disk/wire/configuration formats, or another decision a maintainer may reasonably revisit. Substantial future work starts in `proposed/`; an already-made decision starts in `implemented/`. Updating the note that already owns the decision satisfies the rule; do not create duplicates. Purely mechanical local edits that touch no behavior, contract, structure, process, or rationale are exempt.

An Agent Note is never edited into a **different decision**: supersede it with a new note and keep both cross-linked. Lifecycle migration rules: `proposed/` → `implemented/` rewrites `## Proposal` into a present-tense `## Decision` and folds Acceptance criteria / Risks into `## Consequences`; `proposed/` → `rejected/` only appends the reason to the `Status:` line and freezes the body.

## The file format

### Header block

The first three lines of every Agent Note are exactly:

```markdown
# Agent Note: <title>

Status: <status>
```

followed by a blank line. `Status:` takes exactly one of three forms and must agree with the lifecycle folder:

- `Status: proposed`
- `Status: implemented`
- `Status: rejected — <why, in one line>`

Header tokens (`# Agent Note: ` and the `Status:` line) stay in English verbatim. Notes are written in English by default; a `.zh.md` counterpart may mirror an English note's structure section-for-section.

### Body skeleton

- `proposed/`:

  ```markdown
  ## Problem
  ## Proposal
  …bespoke technical sections…
  ## Alternatives considered
  ## Acceptance criteria
  ## Risks
  ```

- `implemented/`:

  ```markdown
  ## Problem
  ## Decision
  …bespoke technical sections…
  ## Alternatives considered
  ## Consequences
  ```

  `## Decision` describes shipped reality in the present tense; spec-speak headings (`## Proposal`, `## Plan`, `## Migration plan`, `## Acceptance criteria`) must not appear in implemented notes. `## Testing`, `## Deferred`, or `## Related` are allowed where they state present-tense fact.

- `rejected/`: keeps whatever proposal-time skeleton it had, frozen; the verdict lives on the `Status:` line.

### Alternatives considered — mandatory

Every Agent Note carries an `## Alternatives considered` section: each genuine alternative and why it lost — one bold-led paragraph per alternative, or a `### Why not <X>?` subsection for contested ones. A decision recorded without what it beat invites re-litigation — the exact failure Agent Notes exist to prevent. Alternatives are recorded, never invented.
