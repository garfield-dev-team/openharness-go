// Package todo 实现 ReAct agent 的任务规划工具。
// 设计要点：
//  1. todo_write 维护全局 TODO 列表，每 N 轮工具调用后强制 agent 反思并更新 TODO。
//  2. Store 在进程内用互斥锁保护，简化并发安全。
package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// Status 枚举。
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
)

// Item 一条 TODO。
type Item struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

// Store 线程安全的 TODO 仓库，同时跟踪 agent 的工具调用轮次。
type Store struct {
	mu         sync.Mutex
	items      []Item
	toolRounds uint64 // 工具调用计数
	reflectEvery uint64
}

// NewStore 创建新仓库，每 reflectEvery 轮会强制 agent 反思。
func NewStore(reflectEvery int) *Store {
	if reflectEvery <= 0 {
		reflectEvery = 5
	}
	return &Store{reflectEvery: uint64(reflectEvery)}
}

// Snapshot 返回当前 TODO 列表的副本。
func (s *Store) Snapshot() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Item, len(s.items))
	copy(out, s.items)
	return out
}

// IncRound 每次调用一次工具时调用一次，返回是否需要强制反思。
func (s *Store) IncRound() (round uint64, needReflect bool) {
	r := atomic.AddUint64(&s.toolRounds, 1)
	return r, r%s.reflectEvery == 0
}

// Render 把 TODO 列表渲染成适合注入到 system prompt 的 markdown。
func (s *Store) Render() string {
	items := s.Snapshot()
	if len(items) == 0 {
		return "(empty — please call `todo_write` to plan the task)"
	}
	var b strings.Builder
	for _, it := range items {
		marker := "[ ]"
		switch it.Status {
		case StatusCompleted:
			marker = "[x]"
		case StatusInProgress:
			marker = "[~]"
		}
		fmt.Fprintf(&b, "- %s %s: %s\n", marker, it.ID, it.Content)
	}
	return b.String()
}

// UpdateFromJSON 用外部传入的 JSON 全量覆盖 TODO 列表。
func (s *Store) UpdateFromJSON(raw []byte) ([]Item, error) {
	var payload struct {
		Todos []Item `json:"todos"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if len(payload.Todos) == 0 {
		return nil, fmt.Errorf("todos must not be empty")
	}
	for i := range payload.Todos {
		if payload.Todos[i].Status == "" {
			payload.Todos[i].Status = StatusPending
		}
	}
	s.mu.Lock()
	s.items = payload.Todos
	s.mu.Unlock()
	return s.Snapshot(), nil
}

// ---------------------------------------------------------------------------
// todo_write 工具
// ---------------------------------------------------------------------------

type writeTool struct {
	store *Store
}

// NewWriteTool 创建 todo_write 工具。
func NewWriteTool(store *Store) tool.InvokableTool { return &writeTool{store: store} }

func (t *writeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "todo_write",
		Desc: "用一个完整的 todo 列表覆盖当前计划。每当完成阶段性目标或发现新需求时都应调用。status 取值：pending / in_progress / completed。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"todos": {
				Type:     schema.Array,
				Desc:     "完整的待办列表",
				Required: true,
				ElemInfo: &schema.ParameterInfo{
					Type: schema.Object,
					SubParams: map[string]*schema.ParameterInfo{
						"id":      {Type: schema.String, Desc: "唯一标识", Required: true},
						"content": {Type: schema.String, Desc: "任务描述", Required: true},
						"status":  {Type: schema.String, Desc: "状态", Enum: []string{StatusPending, StatusInProgress, StatusCompleted}},
					},
				},
			},
		}),
	}, nil
}

func (t *writeTool) InvokableRun(_ context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	items, err := t.store.UpdateFromJSON([]byte(argsJSON))
	if err != nil {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
		return string(b), nil
	}
	b, _ := json.Marshal(map[string]any{"ok": true, "todos": items})
	return string(b), nil
}
