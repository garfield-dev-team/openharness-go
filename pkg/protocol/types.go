// Package protocol defines the HITL (Human-In-The-Loop) protocol types used
// for bidirectional communication between the backend agent loop and frontends.
//
// This package is kept dependency-free to avoid import cycles between
// pkg/ui, pkg/hitl, and pkg/engine.
package protocol

import (
	"encoding/json"
)

// ---------------------------------------------------------------------------
// Backend → Frontend events (emitted by the engine, consumed by the UI)
// ---------------------------------------------------------------------------

type BackendEventType string

const (
	BEReady             BackendEventType = "ready"
	BEStateSnapshot     BackendEventType = "state_snapshot"
	BETasksSnapshot     BackendEventType = "tasks_snapshot"
	BETranscriptItem    BackendEventType = "transcript_item"
	BEAssistantDelta    BackendEventType = "assistant_delta"
	BEAssistantComplete BackendEventType = "assistant_complete"
	BELineComplete      BackendEventType = "line_complete"
	BEToolStarted       BackendEventType = "tool_started"
	BEToolCompleted     BackendEventType = "tool_completed"
	BEClearTranscript   BackendEventType = "clear_transcript"
	BEError             BackendEventType = "error"
	BEShutdown          BackendEventType = "shutdown"

	// HITL events – require a frontend response to unblock the engine.
	BEModalRequest  BackendEventType = "modal_request"
	BESelectRequest BackendEventType = "select_request"
)

type ModalKind string

const (
	ModalQuestion   ModalKind = "question"
	ModalPermission ModalKind = "permission"
)

type ModalInfo struct {
	Kind      ModalKind `json:"kind"`
	RequestID string    `json:"request_id"`
	Question  string    `json:"question,omitempty"`
	Options   []string  `json:"options,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
	Reason    string    `json:"reason,omitempty"`
}

type BackendEvent struct {
	Type          BackendEventType       `json:"type"`
	Text          string                 `json:"text,omitempty"`
	Error         string                 `json:"error,omitempty"`
	Modal         *ModalInfo             `json:"modal,omitempty"`
	SelectOptions []SelectOption         `json:"select_options,omitempty"`
	Extra         map[string]interface{} `json:"extra,omitempty"`
}

type SelectOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Desc  string `json:"description,omitempty"`
}

func (e *BackendEvent) MarshalJSON() ([]byte, error) {
	type Alias BackendEvent
	return json.Marshal((*Alias)(e))
}

// ---------------------------------------------------------------------------
// Frontend → Backend requests
// ---------------------------------------------------------------------------

type FrontendRequestType string

const (
	FRSubmitLine         FrontendRequestType = "submit_line"
	FRQuestionResponse   FrontendRequestType = "question_response"
	FRPermissionResponse FrontendRequestType = "permission_response"
	FRListSessions       FrontendRequestType = "list_sessions"
	FRShutdown           FrontendRequestType = "shutdown"
)

type FrontendRequest struct {
	Type      FrontendRequestType `json:"type"`
	RequestID string              `json:"request_id,omitempty"`
	Line      string              `json:"line,omitempty"`
	Answer    string              `json:"answer,omitempty"`
	Allowed   *bool               `json:"allowed,omitempty"`
}

func ParseFrontendRequest(data []byte) (*FrontendRequest, error) {
	var req FrontendRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}
