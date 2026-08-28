// Package hashline implements the content-anchor line protocol: each file
// line is prefixed with a short stable hash that the model can reference in
// Edit calls instead of re-typing whole lines. This removes the retry loops
// caused by unique-match search-and-replace editing.
package hashline

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

// charset is the base62 alphabet used for anchors. 62^3 ≈ 238k values is
// ample for single files of a few hundred lines; collisions are resolved by
// requiring a disambiguator (see ErrAmbiguous).
const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// AnchorLen is the number of characters in every anchor.
const AnchorLen = 3

// LineHash computes the stable 3-char anchor of one line. Trailing
// whitespace is trimmed before hashing so unrelated trailing-whitespace
// churn does not invalidate anchors.
func LineHash(line string) string {
	h := fnv.New32a()
	h.Write([]byte(strings.TrimRight(line, " \t\r")))
	v := h.Sum32() % (62 * 62 * 62)
	b := make([]byte, AnchorLen)
	for i := AnchorLen - 1; i >= 0; i-- {
		b[i] = charset[v%62]
		v /= 62
	}
	return string(b)
}

// Encode renders lines in the Read tool wire format: "<hash>|<content>".
func Encode(lines []string) string {
	var sb strings.Builder
	for _, line := range lines {
		sb.WriteString(LineHash(line))
		sb.WriteByte('|')
		sb.WriteString(strings.TrimRight(line, "\r"))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// Index maps every anchor to the sorted line numbers carrying it.
func Index(lines []string) map[string][]int {
	idx := make(map[string][]int)
	for i, line := range lines {
		h := LineHash(line)
		idx[h] = append(idx[h], i)
	}
	return idx
}

// Resolution is one validated anchor: the 0-based line it applies to.
type Resolution struct {
	Anchor string
	Line   int
}

// ResolveError describes why an anchor could not be resolved, phrased as an
// instruction the model can act on directly.
type ResolveError struct {
	Anchor     string
	Stale      bool
	Candidates []int // populated when Stale == false (ambiguous hit)
}

func (e *ResolveError) Error() string {
	if e.Stale {
		return fmt.Sprintf(
			"anchor %q not found in file — the file has changed since your last Read; call Read on this file again and retry the edit with fresh anchors", e.Anchor)
	}
	return fmt.Sprintf(
		"anchor %q matches lines %v — pass \"line\" (the exact line number) or \"occurrence\" (1-based which-match) to disambiguate",
		e.Anchor, e.Candidates)
}

// Request is one pending anchor edit before validation.
type Request struct {
	Anchor     string
	LineHint   int
	Occurrence int
}

// Resolve maps requests onto concrete line numbers using the current file
// index. All requests are validated up front so an edit is all-or-nothing:
// if any anchor fails, no line is touched and the first error is returned.
func Resolve(idx map[string][]int, reqs []Request) ([]Resolution, error) {
	out := make([]Resolution, 0, len(reqs))
	for _, r := range reqs {
		candidates, ok := idx[r.Anchor]
		if !ok || len(candidates) == 0 {
			return nil, &ResolveError{Anchor: r.Anchor, Stale: true}
		}
		line, err := pick(candidates, r)
		if err != nil {
			return nil, err
		}
		out = append(out, Resolution{Anchor: r.Anchor, Line: line})
	}
	return out, nil
}

func pick(candidates []int, r Request) (int, error) {
	switch {
	case r.LineHint > 0:
		for _, c := range candidates {
			if c == r.LineHint-1 { // API is 1-based for the model
				return c, nil
			}
		}
		return 0, &ResolveError{Anchor: r.Anchor, Candidates: candidates}
	case len(candidates) == 1:
		return candidates[0], nil
	case r.Occurrence > 0 && r.Occurrence <= len(candidates):
		return candidates[r.Occurrence-1], nil
	default:
		sorted := append([]int(nil), candidates...)
		sort.Ints(sorted)
		return 0, &ResolveError{Anchor: r.Anchor, Candidates: sorted}
	}
}

// Apply replaces the given (sorted-ascending not required) resolution lines
// with their replacement content and returns the new line slice. Replacements
// are applied bottom-up so earlier edits never shift later line numbers.
func Apply(lines []string, res []Resolution, contentFor func(Resolution) string) []string {
	sorted := append([]Resolution(nil), res...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Line > sorted[j].Line })
	for _, r := range sorted {
		replacement := strings.Split(strings.TrimRight(contentFor(r), "\n"), "\n")
		next := append([]string(nil), lines[:r.Line]...)
		next = append(next, replacement...)
		next = append(next, lines[r.Line+1:]...)
		lines = next
	}
	return lines
}
