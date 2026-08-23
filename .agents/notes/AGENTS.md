# AGENTS.md — Agent Notes

Agent Notes are effectively RFCs written by agents: durable proposals and decision records that preserve rationale, alternatives, consequences, and required verification. Follow the [documentation standard](../../docs/AGENTS.md) and [the Agent Note rules](README.md).

**Every new Agent Note triggers a supersession check.** Search the active tree (`proposed/`, `implemented/`, `rejected/`) for older notes covering the same decision or mechanism, classify any full or partial supersession using [oh-archive-agent-notes](../skills/oh-archive-agent-notes/SKILL.md), and archive every qualifying implemented note in the same PR. Keep partial supersessions active and cross-linked. A new note without a supersession check must not merge.
