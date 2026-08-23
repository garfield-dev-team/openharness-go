---
name: oh-doc-standards
description: Use when writing, moving, reviewing, or auditing documentation in openharness-go — choosing hierarchy and detail, separating tutorials from references, checking tutorial progression, trimming doc slop, or requests like "improve the docs", "audit the docs", "where should this be documented", or "this doc is too long".
---

# Applying the openharness-go Documentation Standard

The documentation rules live in [docs/AGENTS.md](../../../docs/AGENTS.md). This workflow covers placement, corpus audits, and validation. It is guidance, not a script; never treat length alone as a defect.

## Sources of truth (read, don't re-summarize)

- [docs/AGENTS.md](../../../docs/AGENTS.md) — hierarchy, tutorial/reference forms, taxonomy, writing rules, and the slop checklist.
- [.agents/notes/README.md](../../notes/README.md) — when a decision earns an Agent Note, how to file it, and what goes inside one (the header block, per-lifecycle skeleton, and the Alternatives-considered mandate).
- Root [AGENTS.md](../../../AGENTS.md) — standing orders.

## Review structure before prose

Apply the standard's authoring order to every human-facing document. Do not apply this structural pass to Agent Notes; their format is owned by the Agent Note rules instead.

1. Locate the document in the repository and navigation trees. State its own subject and identify its direct children.
2. Set the permitted level of detail. Keep full detail about the document's subject, summarize direct children by purpose, responsibility, and high-level behavior, and move deeper explanations to their owning descendants with links.
3. Classify the document from its intended use, not its path or title. A tutorial must lead through ordered work to an observable outcome; a reference must support lookup within an explicit scope without requiring sequential reading.
4. For a tutorial, privately classify the starting reader and concepts as beginner, intermediate, or advanced. Trace each concept to its prerequisites, reorder premature material, and move optional advanced detail to a later tutorial or reference.
5. Split substantial mixed forms. Put a small secondary form in a clearly labeled section.

Then check constraints that make placement expensive or wrong:

- Decision rationale never lives in topic docs or READMEs — file an Agent Note and link it.
- Before renaming or moving any doc, grep for inbound references (relative markdown links and `#fragment` anchors). A move is atomic: remove from the old home, add to the new home, and fix every inbound link in the same change.
- Every fact has one home per the tier table in docs/AGENTS.md; if you are about to restate something another tier owns, link there instead.

## Audit the corpus

After the structural pass, hunt the slop checklist with the cheapest probes first:

1. Measure outliers: `git ls-files '*.md' | xargs wc -w | sort -rn | head -30`. Length is a signal to investigate, never itself a defect.
2. Hunt reasoning-transcript leakage — narrated history ("previously", "now", PR numbers), design-session citations, control-flow narration, test walkthroughs. Preserve only a non-obvious contract or durable rationale; the same rationale repeated beside sibling items keeps one home.
3. Hunt duplication by grepping distinctive phrases. Keep one home and replace other copies with links.
4. Replace hand-written catalogs, test inventories, and code restatements with the authoritative tree or source.
5. In `implemented/` Agent Notes, remove migration plans, acceptance-task checklists, and future-tense spec language. Keep concise verification contracts plus named coverage gaps.
6. If removing prose changes a promised behavior rather than its explanation, file a proposed Agent Note first.

Exclude `archived/` notes from corpus audits and edits.

Keep every load-bearing rule, preferably as one to three lines plus a link to its rationale. Cut stories, duplicates, status notes, and the path used to derive the rule.

## Validation

Run `git diff --check` and verify every relative markdown link you touched resolves against the tree (grep for target paths — link rot is the failure mode this repo enforces by review). State word deltas for substantially rewritten docs and list any deliberately long exception in the PR description.
