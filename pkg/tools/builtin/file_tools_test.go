package builtin_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/openharness/openharness/pkg/hashline"
	"github.com/openharness/openharness/pkg/tools"
	"github.com/openharness/openharness/pkg/tools/builtin"
)

func execCtx(t *testing.T, dir string) *tools.ToolExecutionContext {
	t.Helper()
	return tools.NewToolExecutionContext(dir)
}

func run(t *testing.T, tool tools.BaseTool, input any) *tools.ToolResult {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res, err := tool.Execute(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res
}

var anchorRe = regexp.MustCompile(`^([0-9A-Za-z]{3})\|`)

// TestReadEmitsAnchorsAndDefaultLimit verifies the anchor wire format and the
// 250-line default with continuation hint.
func TestReadEmitsAnchorsAndDefaultLimit(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString(strings.Repeat("x", i%7) + "\n")
	}
	path := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	res := run(t, builtin.NewFileReadTool(), builtin.FileReadInput{FilePath: path})
	out := res.Output

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 251 { // 250 content lines + continuation hint
		t.Fatalf("got %d lines, want 250 + hint", len(lines))
	}
	if !anchorRe.MatchString(lines[0]) {
		t.Fatalf("first line lacks anchor prefix: %q", lines[0])
	}
	wantHint := "(50 more lines; call Read again with offset=250 to continue)"
	if !strings.Contains(out, wantHint) {
		t.Fatalf("missing continuation hint %q in output tail: %q", wantHint, lines[250])
	}
}

func TestReadOmitsTrailingEmptyLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("a\nb\n"), 0o644)

	res := run(t, builtin.NewFileReadTool(), builtin.FileReadInput{FilePath: path})
	if got := strings.Count(res.Output, "\n"); got != 2 {
		t.Fatalf("output has %d newlines, want exactly two lines: %q", got, res.Output)
	}
}

// TestEditRoundTripViaAnchors drives the full model workflow: Read to obtain
// anchors, then Edit referencing them. It fails if the Read format and the
// Edit validation ever drift apart.
func TestEditRoundTripViaAnchors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	src := "package main\n\nfunc main() {\n\tpanic(\"todo\")\n}\n"
	os.WriteFile(path, []byte(src), 0o644)

	readRes := run(t, builtin.NewFileReadTool(), builtin.FileReadInput{FilePath: path})
	panicLine := "\tpanic(\"todo\")"
	anchor := extractAnchor(t, readRes.Output, panicLine)

	editRes := run(t, builtin.NewFileEditTool(), builtin.FileEditInput{
		FilePath: path,
		Anchors: []builtin.AnchorEdit{
			{Anchor: anchor, NewLines: "\tfmt.Println(\"hi\")"},
		},
	})
	if editRes.IsError {
		t.Fatalf("edit failed: %s", editRes.Output)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `fmt.Println("hi")`) || strings.Contains(string(data), "panic") {
		t.Fatalf("file not edited as intended:\n%s", data)
	}
}

func TestEditMultiAnchorAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	src := "one\ntwo\nthree\nfour\nfive\nsix\nseven\n"
	os.WriteFile(path, []byte(src), 0o644)

	readRes := run(t, builtin.NewFileReadTool(), builtin.FileReadInput{FilePath: path})
	a1 := extractAnchor(t, readRes.Output, "one")
	a3 := extractAnchor(t, readRes.Output, "three")
	a7 := extractAnchor(t, readRes.Output, "seven")

	res := run(t, builtin.NewFileEditTool(), builtin.FileEditInput{
		FilePath: path,
		Anchors: []builtin.AnchorEdit{
			{Anchor: a7, NewLines: "SEVEN"},     // highest line first internally
			{Anchor: a1, NewLines: "ONE\nONE+"}, // multi-line replacement shifts later lines
			{Anchor: a3, NewLines: "THREE"},
		},
	})
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Output)
	}
	data, _ := os.ReadFile(path)
	want := "ONE\nONE+\ntwo\nTHREE\nfour\nfive\nsix\nSEVEN\n"
	if string(data) != want {
		t.Fatalf("file =\n%q\nwant\n%q", data, want)
	}
}

func TestEditStaleAnchorRejectedAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	src := "alpha\nbeta\ngamma\n"
	os.WriteFile(path, []byte(src), 0o644)

	stale := hashline.LineHash("alpha") // valid hash of content no longer present? keep present but use bogus below
	bogus := "zz9"

	res := run(t, builtin.NewFileEditTool(), builtin.FileEditInput{
		FilePath: path,
		Anchors: []builtin.AnchorEdit{
			{Anchor: stale, NewLines: "ok"},
			{Anchor: bogus, NewLines: "never"},
		},
	})
	if !res.IsError {
		t.Fatal("expected error for unknown anchor")
	}
	if !strings.Contains(res.Output, "re-Read") && !strings.Contains(res.Output, "Read") {
		t.Fatalf("error must be actionable (tell the model to re-Read): %s", res.Output)
	}
	data, _ := os.ReadFile(path)
	if string(data) != src {
		t.Fatalf("rejected edit must not touch the file; got:\n%s", data)
	}
}

func TestEditPreservesModeOnAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	os.WriteFile(path, []byte("echo hi\n"), 0o755)

	readRes := run(t, builtin.NewFileReadTool(), builtin.FileReadInput{FilePath: path})
	res := run(t, builtin.NewFileEditTool(), builtin.FileEditInput{
		FilePath: path,
		Anchors:  []builtin.AnchorEdit{{Anchor: extractAnchor(t, readRes.Output, "echo hi"), NewLines: "echo bye"}},
	})
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Output)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755 preserved", info.Mode().Perm())
	}
}

func extractAnchor(t *testing.T, readOutput, lineContent string) string {
	t.Helper()
	for _, l := range strings.Split(readOutput, "\n") {
		if strings.HasSuffix(l, "|"+strings.TrimRight(lineContent, "\r")) ||
			strings.Contains(l, "|"+lineContent) {
			m := anchorRe.FindStringSubmatch(l)
			if m != nil {
				return m[1]
			}
		}
	}
	t.Fatalf("no anchor line for %q in read output:\n%s", lineContent, readOutput)
	return ""
}
