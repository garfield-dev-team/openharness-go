package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// AddMemoryEntry creates or overwrites a memory entry file under the
// project-specific memory directory.
func AddMemoryEntry(cwd, title, content string) error {
	dir := GetProjectMemoryDir(cwd)
	if err := EnsureMemoryDir(dir); err != nil {
		return fmt.Errorf("memory: ensure dir: %w", err)
	}
	filename := sanitiseTitle(title) + ".md"
	path := filepath.Join(dir, filename)
	body := fmt.Sprintf("# %s\n\n%s\n", title, content)
	return os.WriteFile(path, []byte(body), 0o644)
}

// RemoveMemoryEntry removes a memory entry by title.
func RemoveMemoryEntry(cwd, title string) error {
	dir := GetProjectMemoryDir(cwd)
	filename := sanitiseTitle(title) + ".md"
	path := filepath.Join(dir, filename)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("memory: remove: %w", err)
	}
	return nil
}

// ListMemoryFiles returns headers for all memory files in the project dir,
// sorted by modification time descending.
func ListMemoryFiles(cwd string) ([]MemoryHeader, error) {
	dir := GetProjectMemoryDir(cwd)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory: list: %w", err)
	}

	var headers []MemoryHeader
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		title := strings.TrimSuffix(e.Name(), ".md")
		headers = append(headers, MemoryHeader{
			Path:       filepath.Join(dir, e.Name()),
			Title:      title,
			ModifiedAt: info.ModTime(),
		})
	}

	sort.Slice(headers, func(i, j int) bool {
		return headers[i].ModifiedAt.After(headers[j].ModifiedAt)
	})

	return headers, nil
}

// LoadMemoryPrompt reads all memory files for the project and returns a
// combined prompt string suitable for injection into the system prompt.
func LoadMemoryPrompt(cwd string) string {
	headers, err := ListMemoryFiles(cwd)
	if err != nil || len(headers) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, h := range headers {
		data, err := os.ReadFile(h.Path)
		if err != nil {
			continue
		}
		sb.WriteString(strings.TrimSpace(string(data)))
		sb.WriteString("\n\n")
	}
	return strings.TrimSpace(sb.String())
}

// FindRelevantMemories returns memory headers whose title contains the query
// (case-insensitive substring match). A production implementation could use
// embeddings; this version mirrors the simple Python fallback.
func FindRelevantMemories(cwd, query string) ([]MemoryHeader, error) {
	all, err := ListMemoryFiles(cwd)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	var matched []MemoryHeader
	for _, h := range all {
		if strings.Contains(strings.ToLower(h.Title), q) {
			matched = append(matched, h)
		}
	}
	return matched, nil
}

// sanitiseTitle converts a human title to a safe filename stem.
func sanitiseTitle(title string) string {
	// Replace spaces and path-unsafe chars with underscores.
	r := strings.NewReplacer(
		" ", "_", "/", "_", "\\", "_",
		":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_",
	)
	s := r.Replace(title)
	if s == "" {
		s = fmt.Sprintf("entry_%d", time.Now().UnixMilli())
	}
	return s
}
