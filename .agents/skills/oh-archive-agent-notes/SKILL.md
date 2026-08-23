---
name: oh-archive-agent-notes
description: Use when adding, auditing, pruning, archiving, restoring, or reviewing Agent Notes in openharness-go; checks every new note for superseded active records, classifies implemented notes by future decision value, deletes rejected notes that no longer prevent a tempting mistake, and applies the archived-tree rules.
---

# Archiving openharness-go Agent Notes

Reduce the active decision corpus without erasing history that can still guide work. Judge every note semantically; word count and age are discovery aids, never archive criteria.

## Read the contracts

Read [the Agent Note rules](../../notes/README.md) before classifying. Use current code, configuration, package docs, newer Agent Notes, and inbound links to establish whether a rationale still owns or constrains anything.

## Check supersession when adding a note

Every new Agent Note triggers a scoped audit of active notes covering the same decision, mechanism, or rejected alternative. Classify each full or partial supersession while writing the new note:

- Archive qualifying implemented notes in the same PR.
- Retain and cross-link partial supersessions or independently useful rationale.
- Reject obsolete proposals with an honest reason on the `Status:` line.
- Delete rejected notes that no longer prevent a plausible mistake, repairing or deleting inbound links.

Apply the consolidation rule when a new owner absorbs every unique proposition of an older note; do not defer a known match to a later corpus audit.

## Classify by future value

Apply these lifecycle-specific outcomes:

- **Implemented — keep active:** retain a note when its rationale, alternatives, negative guarantees, durable/wire semantics, ownership boundary, security rule, or reintroduction condition is likely to guide a future change. Length does not matter.
- **Implemented — archive:** archive a note when the shipped decision is complete and its body is unlikely to guide future work — one-off CLI chrome, a narrow adapter, a minor closed bug, superseded implementation detail, or process history whose current behavior is obvious elsewhere.
- **Proposed — never archive:** keep a live proposal active; if it is no longer worth pursuing, reject it instead and satisfy the rejected lifecycle format.
- **Rejected — keep only as a guardrail:** retain a rejection only when the losing proposal remains a tempting, meaningful mistake and the note explains why it loses.
- **Rejected — delete:** delete the note when the rejected idea is obsolete, superseded, no longer plausible, or unlikely to prevent re-litigation. Repair or delete inbound links.

Do not archive toward a quota. Inspect every note in scope, classify analogous groups under one principle, use best judgment for close cases, and record genuinely borderline decisions in the PR description.

## Archive one implemented note

1. Move the file from `implemented/<class>/` to `archived/<class>/`; `implemented` is deliberately absent from the archive path. Create `archived/<class>/` on first use.
2. Make no body edits. Insert only `Archived: YYYY-MM-DD` immediately below the `Status: implemented` line, using the archival date.
3. Do not translate, reformat, update facts, or repair links inside the archived note.
4. Search for inbound links from active prose. Redirect them to current authority, retarget them to the archived path only when the historical snapshot is intentionally cited, or delete them. Never verify or repair links out of the archived note.

After archival, never edit, move, or delete the note. Archived notes remain valid inbound-link targets but are historical snapshots, not authority for current behavior.

## Validate and report

Run `go build ./...`, `go vet ./...`, `go test ./...`, and `git diff --check`. Verify every relative markdown link you touched resolves (grep for the target paths).

Report active implemented notes kept, implemented notes archived, rejected notes kept/deleted, proposed notes rejected if any, and every genuinely borderline case with its chosen outcome.
