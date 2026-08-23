package session_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openharness/openharness/pkg/session"
	"github.com/openharness/openharness/pkg/types"
)

func openStore(t *testing.T, path string) *session.Store {
	t.Helper()
	s, err := session.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAppendRoundTripAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	s := openStore(t, path)

	s.AppendMessage(types.FromUserText("hello"))
	s.AppendMessage(types.FromUserText("world"))

	got := s.ActiveMessages()
	if len(got) != 2 || got[0].GetText() != "hello" || got[1].GetText() != "world" {
		t.Fatalf("unexpected active messages: %+v", got)
	}

	// Reload from disk and verify identical history.
	s2 := openStore(t, path)
	reloaded := s2.ActiveMessages()
	if len(reloaded) != 2 || reloaded[1].GetText() != "world" {
		t.Fatalf("reload mismatch: %+v", reloaded)
	}
	if s2.LeafID() == "" {
		t.Fatal("reloaded store has empty leaf")
	}
}

func TestNavigateToForksTree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s2.jsonl")
	s := openStore(t, path)

	first, _ := s.AppendMessage(types.FromUserText("turn 1"))
	second, _ := s.AppendMessage(types.FromUserText("turn 2a"))

	// Jump back to the first entry; the next append forks the tree.
	if err := s.NavigateTo(first.ID); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	alt, _ := s.AppendMessage(types.FromUserText("turn 2b"))

	pathEntries := s.ActivePath()
	if len(pathEntries) != 2 ||
		pathEntries[0].ID != first.ID ||
		pathEntries[1].ID != alt.ID {
		t.Fatalf("active path should be [first, alt], got %d entries", len(pathEntries))
	}
	// The abandoned branch is still present in the tree.
	if s.Len() != 3 || second.ParentID != first.ID {
		t.Fatalf("tree lost the abandoned branch: len=%d", s.Len())
	}
	if err := s.NavigateTo("nonexistent"); err == nil {
		t.Fatal("navigate to unknown entry should fail")
	}
}

func TestTornTailRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s3.jsonl")
	s := openStore(t, path)
	s.AppendMessage(types.FromUserText("good one"))
	s.AppendMessage(types.FromUserText("good two"))
	s.Close()

	// Simulate a crash mid-append: append a partial JSON line.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("reopen for corruption: %v", err)
	}
	if _, err := f.WriteString(`{"id":"torn","paren`); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	f.Close()

	s2 := openStore(t, path)
	msgs := s2.ActiveMessages()
	if len(msgs) != 2 || msgs[1].GetText() != "good two" {
		t.Fatalf("torn tail not recovered: %+v", msgs)
	}

	// New appends must stay parseable after recovery.
	s2.AppendMessage(types.FromUserText("after crash"))
	s2.Close()

	s3 := openStore(t, path)
	if got := s3.ActiveMessages(); len(got) != 3 || got[2].GetText() != "after crash" {
		t.Fatalf("post-recovery append broken: %+v", got)
	}
}

func TestForkToCopiesActiveBranch(t *testing.T) {
	basePath := filepath.Join(t.TempDir(), "base.jsonl")
	forkPath := filepath.Join(t.TempDir(), "fork.jsonl")

	s := openStore(t, basePath)
	s.AppendMessage(types.FromUserText("one"))
	leafOne, _ := s.AppendMessage(types.FromUserText("two"))
	s.NavigateTo(leafOne.ParentID) // park on "one"; "two" leaves active path
	s.AppendMessage(types.FromUserText("three"))

	fork, err := s.ForkTo(forkPath)
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	defer fork.Close()

	got := fork.ActiveMessages()
	if len(got) != 2 || got[0].GetText() != "one" || got[1].GetText() != "three" {
		t.Fatalf("fork content mismatch: %+v", got)
	}
	// Forking must not disturb the source store.
	if src := s.ActiveMessages(); len(src) != 2 || src[1].GetText() != "three" {
		t.Fatalf("source mutated by fork: %+v", src)
	}
}

func TestListIDsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	old := openStore(t, filepath.Join(session.Dir(dir), "session_100.jsonl"))
	old.AppendMessage(types.FromUserText("old"))
	old.Close()

	new := openStore(t, filepath.Join(session.Dir(dir), "session_200.jsonl"))
	new.AppendMessage(types.FromUserText("new"))
	new.Close()

	ids, err := session.ListIDs(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) != 2 || ids[0] != "session_200" {
		t.Fatalf("expected newest first without extension, got %v", ids)
	}

	if !session.Exists(dir, "session_200") {
		t.Fatal("Exists should find the stored session")
	}
}
