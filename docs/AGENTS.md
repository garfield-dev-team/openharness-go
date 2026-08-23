# AGENTS.md — The documentation standard

This file defines document structure, tier taxonomy, and writing rules for this repository. Agent Note storage and format rules live in [.agents/notes/README.md](../.agents/notes/README.md); directory-level instructions for notes live in [../.agents/notes/AGENTS.md](../.agents/notes/AGENTS.md).

## Tiers: one home per fact

Each fact belongs to the tier whose job it is; everywhere else, link there instead of restating.

| Tier | Job | Does NOT belong there |
|---|---|---|
| Root `AGENTS.md` | Standing orders: rules an agent needs in context every session, one to three lines each, linking to the fact's home | Stories, worked examples, situational procedures, anything restated from a linked home |
| Subtree `AGENTS.md` (`docs/`, `.agents/notes/`, …) | Orders specific to that subtree | Repo-wide rules the root file already carries |
| [Agent Notes](../.agents/notes/README.md) | Active decision records: the why, what-was-given-up, and required verification; implemented notes describe shipped reality in present tense | Migration plans, acceptance checklists, spec-speak ("should…") once shipped; rejected proposals go to `rejected/` |
| Topic docs in `docs/` (e.g. `context_compaction_and_caching.md`) | Human-facing mechanism explanations and references: tutorials follow an ordered path to an outcome, references define a lookup scope describing current behavior | Decision rationale (→ Agent Notes), standing orders (→ root `AGENTS.md`) |
| Package READMEs (`pkg/*/`) | That package's contract: configuration, semantics, limitations, extension points | Restating godoc, other packages' concerns |

Placement rule of thumb: defect postmortems → topic docs or an Agent Note's Problem section; rationale → Agent Notes; step-by-step procedures → tutorial-style topic docs; type definitions → the declaring package's README or owning topic doc; standing orders → root `AGENTS.md` with a rationale link.

## Writing rules

- **Document current state, not change history.** Durable prose avoids "previously / now / no longer", PR numbers, commits, and code-position narration; name the live mechanism. Change stories go in commits, PR descriptions, or Agent Notes.
- **Every non-trivial change includes at least one Agent Note in the same PR** — update the owning note or add one; purely mechanical local edits are exempt ([scope](../.agents/notes/README.md#when-to-write-one)).
- **One physical line per paragraph**; rely on editor soft-wrap. Code blocks, tables, and list structure keep their own formatting.
- **Comments and docs state complete contracts, not reasoning transcripts.** Keep behavior, failure modes, timing, ownership, and consequences; delete derivation paths, test walkthroughs, and proofs of obvious branches.
- **Write concretely**: name specific components, checks, APIs, and behaviors instead of metaphorical "gates" or "surfaces".
- **Pairs update together**: the same change that reshapes a documented type updates its owning page.

## Slop checklist

After writing any doc, hunt:

- The same rule stated in more than one home. Grep a distinctive phrase; keep one home and link the rest.
- Narrated history: "previously", "used to", "was moved", PR numbers, commits. State the current fact; link an Agent Note when needed.
- Implementation-status annotations ("implemented!", "future: …"). Status rots; repo layout and code carry it.
- Hand-restated catalogs, test/package inventories — wherever source is authoritative.
- Reasoning transcripts: step-by-step implementation narration, proofs of obvious branches. Keep the resulting contract; cut the derivation.
- Paragraph walls: one paragraph carrying several rules and parenthetical asides. Split it or demote detail to its home.
- Emphasis inflation: bold everywhere means nothing stands out. Reserve emphasis for the clause that changes behavior.
- Spec-speak in `implemented/` notes: "should", migration plans, acceptance checklists. Implemented describes what is.

## Cross-reference with machine-checkable relative links

Repository references always use relative Markdown paths, never bare filenames or note numbers. A missing link target is a defect; when moving a referenced file, repair every inbound link in the same change.
