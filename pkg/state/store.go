// Package state provides application state management.
package state

import "sync"

// AppState holds the runtime application state, mirroring Python state/app_state.py.
type AppState struct {
	Model          string
	PermissionMode string
	Theme          string
	Cwd            string
	Provider       string
	AuthStatus     string
	BaseURL        string
	VimEnabled     bool
	VoiceEnabled   bool
	FastMode       bool
	Effort         string
	Passes         int
	McpConnected   int
	McpFailed      int
	OutputStyle    string
}

// AppStateStore is a thread-safe container for AppState with pub/sub.
type AppStateStore struct {
	mu          sync.RWMutex
	state       AppState
	subscribers []func(AppState)
}

// NewAppStateStore creates an AppStateStore with the given initial state.
func NewAppStateStore(initial AppState) *AppStateStore {
	return &AppStateStore{state: initial}
}

// Get returns a snapshot of the current state.
func (s *AppStateStore) Get() AppState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Set replaces the entire state and notifies subscribers.
func (s *AppStateStore) Set(st AppState) {
	s.mu.Lock()
	s.state = st
	subs := make([]func(AppState), len(s.subscribers))
	copy(subs, s.subscribers)
	s.mu.Unlock()

	for _, fn := range subs {
		fn(st)
	}
}

// Update applies a mutator function to the current state and notifies subscribers.
func (s *AppStateStore) Update(fn func(*AppState)) {
	s.mu.Lock()
	fn(&s.state)
	st := s.state
	subs := make([]func(AppState), len(s.subscribers))
	copy(subs, s.subscribers)
	s.mu.Unlock()

	for _, sub := range subs {
		sub(st)
	}
}

// Subscribe registers a callback that is invoked whenever the state changes.
// It returns an unsubscribe function.
func (s *AppStateStore) Subscribe(fn func(AppState)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribers = append(s.subscribers, fn)
	idx := len(s.subscribers) - 1
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// nil-out rather than slice-remove to keep indices stable
		if idx < len(s.subscribers) {
			s.subscribers[idx] = nil
		}
	}
}

// Notify manually fires all subscribers with the current state.
func (s *AppStateStore) Notify() {
	s.mu.RLock()
	st := s.state
	subs := make([]func(AppState), len(s.subscribers))
	copy(subs, s.subscribers)
	s.mu.RUnlock()

	for _, fn := range subs {
		if fn != nil {
			fn(st)
		}
	}
}
