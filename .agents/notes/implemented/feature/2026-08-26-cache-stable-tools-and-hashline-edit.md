# Agent Note: deterministic tool schema order + hashline anchor editing

Date: 2026-08-26
Status: implemented
Scope: pkg/tools/base.go, pkg/hashline, pkg/tools/builtin/file_read.go, pkg/tools/builtin/file_edit.go
Related (context, **not** superseded): [pi/omp implementation design](../../proposed/architecture/2026-08-22-pi-omp-implementation-design.md) — this note lands its §8.1 ordering fix and §1 Hashline protocol; the design note stays active for §8.2/§4–§10.

Supersession check 2026-08-26 across `proposed/`, `implemented/`: no prior note owns tool-array determinism or line-anchor editing.

## Decisions

1. **Tool array determinism (design §8.1)**: `ToolRegistry.ListTools` and
   `ToAPISchema` now sort by tool name. Previously map iteration randomized
   the tools array every request, invalidating provider prompt-cache
   prefixes on every turn. Skills/memory inputs to
   `BuildRuntimeSystemPrompt` were audited and are already deterministic
   (`os.ReadDir` sorts; memory headers sorted). Regression:
   `TestToAPISchemaDeterministicOrder` asserts identical order across 50
   calls plus ascending sort — fails if anyone reverts to map iteration.
   Usage-token parsing completion (input/cache tokens) is *not* part of
   this change; it remains open under P0-5b.

2. **Read emits anchors (§1.2)**: every Read output line is prefixed with a
   3-char base62 FNV hash of the trailing-whitespace-trimmed line:
   `aB9|func main() {`. Default limit tightened to 250 lines with an
   explicit continuation hint (`offset=N`) so truncation is actionable.
   Trailing newline is no longer reported as an empty final line.
   Tool description tells the model to reference anchors instead of
   re-typing lines.

3. **Edit is all-or-nothing anchors (§1.3)**: input is now
   `{file_path, anchors:[{anchor, line?, occurrence?, new_lines}]}`.
   Validation runs against a fresh index of the current file before any
   write: stale anchor → error instructing a fresh Read; ambiguous → error
   listing candidate line numbers and naming both disambiguators. The old
   `old_string`/`new_string` unique-match protocol is removed (it was the
   retry-loop source called out in docs/p0-tech-debt.md); no other code
   referenced it.

4. **Multi-line replacement applied bottom-up**: replacements are sorted by
   descending line number before application, so earlier insertions never
   shift later anchors within one call. Multi-line `new_lines` is supported.

5. **Atomic write**: temp file in the target directory + rename, preserving
   the original permission bits; deferred cleanup removes the temp file on
   failure paths.

## Consequences

- `TestEditRoundTripViaAnchors` drives the real model workflow (Read →
  extract anchor from output → Edit with it), so the two wire formats
  cannot drift silently. `TestEditStaleAnchorRejectedAtomically` asserts
  rejected edits never touch the file. `TestEditPreservesModeOnAtomicWrite`
  pins mode preservation. Collision sanity for the 62^3 space is guarded by
  `TestCollisionRateSanity` (500 distinct lines).
- Anchors are content-derived and position-free: edits by other processes
  between Read and Edit surface as stale-anchor errors rather than silent
  corruption of the wrong line.
- The model must copy anchors verbatim; malformed-length anchors get an
  error pointing at the Read output prefix format.
