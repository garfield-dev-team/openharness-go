// Package session implements durable tree-structured conversation storage.
//
// A session is an append-only JSONL file where every Entry links to its
// parent, forming a tree. The active branch is defined by the current leaf;
// ActivePath walks from the leaf back to the root to produce the linear
// message sequence the LLM actually sees.
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openharness/openharness/pkg/internal/uid"
	"github.com/openharness/openharness/pkg/types"
)

// EntryKind discriminates what an Entry carries in its payload.
type EntryKind string

const (
	KindMessage       EntryKind = "message"        // user/assistant/tool-result message
	KindCompaction    EntryKind = "compaction"     // summary checkpoint
	KindBranchSummary EntryKind = "branch_summary" // summary of a pruned branch
	KindStreamRule    EntryKind = "stream_rule"    // injected rule record, survives compaction
)

// Entry is a single node in the session tree.
type Entry struct {
	ID       string                     `json:"id"`
	ParentID string                     `json:"parentId"` // "" only for the root
	Ts       int64                      `json:"ts"`
	Kind     EntryKind                  `json:"kind"`
	Msg      *types.ConversationMessage `json:"msg,omitempty"`
	Meta     map[string]any             `json:"meta,omitempty"`
}

// Store is a durable session tree backed by an append-only JSONL file.
// All methods are safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	f      *os.File
	path   string
	byID   map[string]*Entry
	order  []string // entry IDs in file order
	leafID string   // current active branch leaf ("" = empty tree)
}

// New returns an in-memory-only store (Close is a no-op). Useful for tests.
func New() *Store {
	return &Store{byID: map[string]*Entry{}}
}

// Dir returns the directory holding session files for a working directory.
func Dir(cwd string) string {
	return filepath.Join(cwd, ".openharness", "sessions")
}

// FilePath returns the JSONL path for a session ID.
func FilePath(cwd, id string) string {
	return filepath.Join(Dir(cwd), id+".jsonl")
}

// Exists reports whether a session file is present for the given ID.
func Exists(cwd, id string) bool {
	_, err := os.Stat(FilePath(cwd, id))
	return err == nil
}

// ListIDs returns known session IDs for a working directory, newest first.
func ListIDs(cwd string) ([]string, error) {
	entries, err := os.ReadDir(Dir(cwd))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	type named struct {
		id    string
		mtime time.Time
	}
	var found []named
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		found = append(found, named{
			id:    strings.TrimSuffix(filepath.Base(e.Name()), ".jsonl"),
			mtime: info.ModTime(),
		})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].mtime.After(found[j].mtime) })
	ids := make([]string, len(found))
	for i, n := range found {
		ids[i] = n.id
	}
	return ids, nil
}

// Open loads the session file at path, creating it if missing. A torn final
// line (crash mid-append) is tolerated: the file is truncated back to the
// last fully decoded entry so subsequent appends stay parseable.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("session: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("session: open %s: %w", path, err)
	}
	s := &Store{f: f, path: path, byID: map[string]*Entry{}}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var lastGood int64
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		lastGood += int64(len(scanner.Bytes())) + 1
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			// Torn tail: roll the file back to the last complete entry.
			if tErr := f.Truncate(lastGood - int64(len(scanner.Bytes())) - 1); tErr != nil {
				f.Close()
				return nil, fmt.Errorf("session: recover torn tail in %s: %w", path, tErr)
			}
			break
		}
		cp := e
		s.byID[e.ID] = &cp
		s.order = append(s.order, e.ID)
		s.leafID = e.ID
	}
	if err := scanner.Err(); err != nil {
		f.Close()
		return nil, fmt.Errorf("session: scan %s: %w", path, err)
	}
	if _, err := f.Seek(0, 2); err != nil {
		f.Close()
		return nil, fmt.Errorf("session: seek %s: %w", path, err)
	}
	return s, nil
}

// Append writes e to the end of the active branch and advances the leaf.
// Missing ID/Ts are generated; ParentID is always forced to the current leaf.
func (s *Store) Append(e Entry) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.ID == "" {
		e.ID = uid.NewHex()
	}
	if e.Ts == 0 {
		e.Ts = time.Now().UnixMilli()
	}
	e.ParentID = s.leafID

	data, err := json.Marshal(e)
	if err != nil {
		return Entry{}, fmt.Errorf("session: encode entry: %w", err)
	}
	if s.f != nil {
		if _, err := s.f.Write(append(data, '\n')); err != nil {
			return Entry{}, fmt.Errorf("session: append: %w", err)
		}
	}
	cp := e
	s.byID[e.ID] = &cp
	s.order = append(s.order, e.ID)
	s.leafID = e.ID
	return e, nil
}

// AppendMessage appends a conversation message to the active branch.
func (s *Store) AppendMessage(msg types.ConversationMessage) (Entry, error) {
	m := msg
	return s.Append(Entry{Kind: KindMessage, Msg: &m})
}

// ActivePath returns the entries from root to the current leaf.
func (s *Store) ActivePath() []*Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activePathLocked()
}

func (s *Store) activePathLocked() []*Entry {
	var path []*Entry
	for id := s.leafID; id != ""; {
		e, ok := s.byID[id]
		if !ok {
			break
		}
		path = append(path, e)
		id = e.ParentID
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// ActiveMessages returns the conversation messages on the active branch.
func (s *Store) ActiveMessages() []types.ConversationMessage {
	entries := s.ActivePath()
	msgs := make([]types.ConversationMessage, 0, len(entries))
	for _, e := range entries {
		if e.Kind == KindMessage && e.Msg != nil {
			msgs = append(msgs, *e.Msg)
		}
	}
	return msgs
}

// LeafID returns the ID of the current active leaf ("" for an empty tree).
func (s *Store) LeafID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leafID
}

// NavigateTo switches the active branch by pointing the leaf at any existing
// entry — an O(1) tree jump. Later appends fork the tree at that point.
func (s *Store) NavigateTo(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; !ok {
		return fmt.Errorf("session: navigate: unknown entry %s", id)
	}
	s.leafID = id
	return nil
}

// Len returns the total number of entries across all branches.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.order)
}

// ForkTo copies the active branch into a new independent session file and
// returns the new store. The source store is unaffected.
func (s *Store) ForkTo(path string) (*Store, error) {
	ns, err := Open(path)
	if err != nil {
		return nil, err
	}
	for _, e := range s.ActivePath() {
		if _, err := ns.Append(*e); err != nil {
			ns.Close()
			return nil, fmt.Errorf("session: fork: %w", err)
		}
	}
	return ns, nil
}

// Close releases the underlying file handle.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}
