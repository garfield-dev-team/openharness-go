package tasks

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type TaskRegistry struct {
	mu      sync.RWMutex
	tasks   map[string]*TaskEntry
	counter atomic.Int64
}

func NewTaskRegistry() *TaskRegistry {
	return &TaskRegistry{
		tasks: make(map[string]*TaskEntry),
	}
}

func (r *TaskRegistry) generateID() string {
	n := r.counter.Add(1)
	return fmt.Sprintf("task_%x_%04d", time.Now().UnixMilli(), n)
}

func (r *TaskRegistry) Create(prompt string, agentType string, packet *TaskPacket) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := r.generateID()
	now := time.Now()
	entry := &TaskEntry{
		ID:        id,
		Prompt:    prompt,
		Status:    TaskCreated,
		AgentType: agentType,
		Output:    []string{},
		Messages:  []TaskMessage{},
		Packet:    packet,
		CreatedAt: now,
		UpdatedAt: now,
	}
	r.tasks[id] = entry
	return id, nil
}

func (r *TaskRegistry) Get(id string) (*TaskEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task %s not found", id)
	}
	return entry, nil
}

func (r *TaskRegistry) List() []*TaskEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*TaskEntry, 0, len(r.tasks))
	for _, e := range r.tasks {
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}

func (r *TaskRegistry) Stop(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	if entry.Status != TaskRunning && entry.Status != TaskCreated {
		return fmt.Errorf("task %s is not running (status=%s)", id, entry.Status)
	}
	if entry.CancelFunc != nil {
		entry.CancelFunc()
	}
	entry.Status = TaskStopped
	entry.UpdatedAt = time.Now()
	return nil
}

func (r *TaskRegistry) SetStatus(id string, status TaskStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	entry.Status = status
	entry.UpdatedAt = time.Now()
	return nil
}

func (r *TaskRegistry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tasks[id]; !ok {
		return fmt.Errorf("task %s not found", id)
	}
	delete(r.tasks, id)
	return nil
}

func (r *TaskRegistry) AppendOutput(id string, line string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	entry.Output = append(entry.Output, line)
	entry.UpdatedAt = time.Now()
	return nil
}

func (r *TaskRegistry) GetOutput(id string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.tasks[id]
	if !ok {
		return "", fmt.Errorf("task %s not found", id)
	}
	return strings.Join(entry.Output, "\n"), nil
}

func (r *TaskRegistry) SendMessage(id string, msg TaskMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	entry.Messages = append(entry.Messages, msg)
	entry.UpdatedAt = time.Now()
	return nil
}

func (r *TaskRegistry) GetMessages(id string) ([]TaskMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task %s not found", id)
	}
	out := make([]TaskMessage, len(entry.Messages))
	copy(out, entry.Messages)
	return out, nil
}

func (r *TaskRegistry) AssignTeam(id string, teamID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	entry.TeamID = teamID
	entry.UpdatedAt = time.Now()
	return nil
}

func (r *TaskRegistry) setCancelFunc(id string, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.tasks[id]; ok {
		entry.CancelFunc = cancel
	}
}

func (r *TaskRegistry) SetError(id string, errMsg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	entry.Error = errMsg
	entry.UpdatedAt = time.Now()
	return nil
}
