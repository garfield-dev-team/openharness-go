package hitl

import (
	"context"
	"fmt"
	"sync"

	"github.com/openharness/openharness/pkg/protocol"
)

// Manager tracks outstanding HITL requests for a single session.
type Manager struct {
	mu             sync.Mutex
	questionReqs   map[string]chan string
	permissionReqs map[string]chan bool
	emitFn         func(event *protocol.BackendEvent)
	idSeq          uint64
}

func NewManager(emitFn func(event *protocol.BackendEvent)) *Manager {
	return &Manager{
		questionReqs:   make(map[string]chan string),
		permissionReqs: make(map[string]chan bool),
		emitFn:         emitFn,
	}
}

func (m *Manager) nextID() string {
	m.idSeq++
	return fmt.Sprintf("hitl_%d", m.idSeq)
}

// AskQuestion blocks until the frontend responds or ctx is cancelled.
// options non-empty → multiple-choice; empty → free-form.
func (m *Manager) AskQuestion(ctx context.Context, question string, options []string) (string, error) {
	m.mu.Lock()
	reqID := m.nextID()
	ch := make(chan string, 1)
	m.questionReqs[reqID] = ch
	m.mu.Unlock()

	m.emitFn(&protocol.BackendEvent{
		Type: protocol.BEModalRequest,
		Modal: &protocol.ModalInfo{
			Kind:      protocol.ModalQuestion,
			RequestID: reqID,
			Question:  question,
			Options:   options,
		},
	})

	select {
	case answer := <-ch:
		return answer, nil
	case <-ctx.Done():
		m.mu.Lock()
		delete(m.questionReqs, reqID)
		m.mu.Unlock()
		return "", ctx.Err()
	}
}

// AskPermission blocks until the frontend allows/denies.
func (m *Manager) AskPermission(ctx context.Context, toolName, reason string) (bool, error) {
	m.mu.Lock()
	reqID := m.nextID()
	ch := make(chan bool, 1)
	m.permissionReqs[reqID] = ch
	m.mu.Unlock()

	m.emitFn(&protocol.BackendEvent{
		Type: protocol.BEModalRequest,
		Modal: &protocol.ModalInfo{
			Kind:      protocol.ModalPermission,
			RequestID: reqID,
			ToolName:  toolName,
			Reason:    reason,
		},
	})

	select {
	case allowed := <-ch:
		return allowed, nil
	case <-ctx.Done():
		m.mu.Lock()
		delete(m.permissionReqs, reqID)
		m.mu.Unlock()
		return false, ctx.Err()
	}
}

func (m *Manager) ResolveQuestion(requestID, answer string) bool {
	m.mu.Lock()
	ch, ok := m.questionReqs[requestID]
	if ok {
		delete(m.questionReqs, requestID)
	}
	m.mu.Unlock()
	if !ok {
		return false
	}
	ch <- answer
	return true
}

func (m *Manager) ResolvePermission(requestID string, allowed bool) bool {
	m.mu.Lock()
	ch, ok := m.permissionReqs[requestID]
	if ok {
		delete(m.permissionReqs, requestID)
	}
	m.mu.Unlock()
	if !ok {
		return false
	}
	ch <- allowed
	return true
}

func (m *Manager) HandleFrontendRequest(req *protocol.FrontendRequest) error {
	switch req.Type {
	case protocol.FRQuestionResponse:
		if req.RequestID == "" {
			return fmt.Errorf("question_response: missing request_id")
		}
		if !m.ResolveQuestion(req.RequestID, req.Answer) {
			return fmt.Errorf("question_response: unknown request_id %q", req.RequestID)
		}
		return nil
	case protocol.FRPermissionResponse:
		if req.RequestID == "" {
			return fmt.Errorf("permission_response: missing request_id")
		}
		allowed := req.Allowed != nil && *req.Allowed
		if !m.ResolvePermission(req.RequestID, allowed) {
			return fmt.Errorf("permission_response: unknown request_id %q", req.RequestID)
		}
		return nil
	default:
		return fmt.Errorf("unhandled frontend request type: %s", req.Type)
	}
}

func (m *Manager) PendingCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.questionReqs) + len(m.permissionReqs)
}