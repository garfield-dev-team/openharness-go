package hashline_test

import (
	"strings"
	"testing"

	"github.com/openharness/openharness/pkg/hashline"
)

func TestLineHashStableAndTrailingWhitespaceInsensitive(t *testing.T) {
	a := hashline.LineHash("func main() {")
	if len(a) != 3 {
		t.Fatalf("anchor length = %d, want 3", len(a))
	}
	if a != hashline.LineHash("func main() {  \t") {
		t.Fatal("trailing whitespace changed the anchor")
	}
	if a == hashline.LineHash("func main() { x") {
		t.Fatal("different content produced identical anchor")
	}
	for _, c := range a {
		const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
		if !strings.ContainsRune(charset, c) {
			t.Fatalf("anchor %q contains non-base62 char %q", a, c)
		}
	}
}

// TestCollisionRateSanity guards against a broken hash: on a realistic file,
// identical lines collide (expected) but distinct lines must mostly not.
func TestCollisionRateSanity(t *testing.T) {
	lines := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		lines = append(lines, strings.Repeat("x", i+1))
	}
	idx := hashline.Index(lines)
	collisions := 0
	for _, v := range idx {
		if len(v) > 1 {
			collisions += len(v) - 1
		}
	}
	if collisions > 5 {
		t.Fatalf("unexpected collision rate: %d collisions over 500 distinct lines", collisions)
	}
}

func TestEncodeFormat(t *testing.T) {
	out := hashline.Encode([]string{"alpha", "beta"})
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], hashline.LineHash("alpha")+"|alpha") {
		t.Fatalf("Encode output malformed: %q", out)
	}
}

func TestResolveUnambiguous(t *testing.T) {
	idx := hashline.Index([]string{"one", "two", "three"})
	res, err := hashline.Resolve(idx, []hashline.Request{{Anchor: hashline.LineHash("two")}})
	if err != nil || len(res) != 1 || res[0].Line != 1 {
		t.Fatalf("Resolve = %v, %v; want line 1", res, err)
	}
}

func TestResolveStaleAnchorActionable(t *testing.T) {
	idx := hashline.Index([]string{"one", "two"})
	_, err := hashline.Resolve(idx, []hashline.Request{{Anchor: "zzz"}})
	if err == nil {
		t.Fatal("expected stale anchor error")
	}
	if !strings.Contains(err.Error(), "Read") {
		t.Fatalf("stale error must tell the model to re-Read: %v", err)
	}
}

func TestResolveAmbiguousRequiresDisambiguator(t *testing.T) {
	lines := []string{"same", "other", "same", "same"}
	idx := hashline.Index(lines)
	h := hashline.LineHash("same")

	_, err := hashline.Resolve(idx, []hashline.Request{{Anchor: h}})
	if err == nil || !strings.Contains(err.Error(), "occurrence") {
		t.Fatalf("ambiguous anchor must ask for disambiguation: %v", err)
	}

	res, err := hashline.Resolve(idx, []hashline.Request{
		{Anchor: h, Occurrence: 2},
		{Anchor: h, LineHint: 4},
	})
	if err != nil || res[0].Line != 2 || res[1].Line != 3 {
		t.Fatalf("disambiguated resolve = %v, %v", res, err)
	}
}

func TestApplyMultiLineBottomUp(t *testing.T) {
	lines := []string{"a", "b", "c", "d"}
	res := []hashline.Resolution{
		{Anchor: "a1", Line: 0},
		{Anchor: "c3", Line: 2},
	}
	got := hashline.Apply(lines, res, func(r hashline.Resolution) string {
		if r.Line == 0 {
			return "A1\nA2" // multi-line replacement
		}
		return "C"
	})
	want := []string{"A1", "A2", "b", "C", "d"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Apply = %v, want %v", got, want)
		}
	}
}
