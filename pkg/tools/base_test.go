package tools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openharness/openharness/pkg/tools"
)

type fakeTool struct {
	name string
}

func (f *fakeTool) Name() string                      { return f.name }
func (f *fakeTool) Description() string               { return "desc " + f.name }
func (f *fakeTool) InputSchema() map[string]any       { return map[string]any{"type": "object"} }
func (f *fakeTool) IsReadOnly(_ json.RawMessage) bool { return true }
func (f *fakeTool) Execute(_ context.Context, _ json.RawMessage, _ *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	return tools.NewToolResult(""), nil
}

func (f *fakeTool) ToAPISchema() map[string]any {
	return map[string]any{"name": f.name, "description": "desc " + f.name}
}

// TestToAPISchemaDeterministicOrder fails if registry ordering ever reverts
// to map iteration order: provider prompt caches break when the tools array
// changes between turns of an identical conversation.
func TestToAPISchemaDeterministicOrder(t *testing.T) {
	reg := tools.NewToolRegistry()
	for _, n := range []string{"Zeta", "alpha", "Mid", "bash", "Read", "Edit"} {
		if err := reg.Register(&fakeTool{name: n}); err != nil {
			t.Fatalf("register %s: %v", n, err)
		}
	}

	var first []string
	for i := 0; i < 50; i++ {
		schemas := reg.ToAPISchema()
		names := make([]string, 0, len(schemas))
		for _, s := range schemas {
			names = append(names, s["name"].(string))
		}
		if i == 0 {
			first = names
			continue
		}
		for j := range names {
			if names[j] != first[j] {
				t.Fatalf("call %d: order changed at %d: %v vs %v", i, j, first, names)
			}
		}
	}

	for j := 1; j < len(first); j++ {
		if first[j-1] >= first[j] {
			t.Fatalf("tools not sorted ascending: %v", first)
		}
	}
}

func TestListToolsSorted(t *testing.T) {
	reg := tools.NewToolRegistry()
	for _, n := range []string{"mango", "apple", "zebra"} {
		_ = reg.Register(&fakeTool{name: n})
	}
	list := reg.ListTools()
	got := make([]string, 0, len(list))
	for _, tl := range list {
		got = append(got, tl.Name())
	}
	want := []string{"apple", "mango", "zebra"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ListTools = %v, want %v", got, want)
		}
	}
}
