package tasks

import (
	"context"
	"time"
)

type TaskStatus string

const (
	TaskCreated   TaskStatus = "created"
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskStopped   TaskStatus = "stopped"
)

type TaskPacket struct {
	Objective         string   `json:"objective"`
	Scope             []string `json:"scope"`
	Repo              string   `json:"repo,omitempty"`
	BranchPolicy      string   `json:"branch_policy,omitempty"`
	AcceptanceTests   []string `json:"acceptance_tests,omitempty"`
	CommitPolicy      string   `json:"commit_policy,omitempty"`
	ReportingContract string   `json:"reporting_contract,omitempty"`
	EscalationPolicy  string   `json:"escalation_policy,omitempty"`
}

type TaskEntry struct {
	ID         string             `json:"id"`
	Prompt     string             `json:"prompt"`
	Status     TaskStatus         `json:"status"`
	AgentID    string             `json:"agent_id,omitempty"`
	AgentType  string             `json:"agent_type,omitempty"`
	TeamID     string             `json:"team_id,omitempty"`
	Output     []string           `json:"output"`
	Messages   []TaskMessage      `json:"messages"`
	Packet     *TaskPacket        `json:"packet,omitempty"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
	Error      string             `json:"error,omitempty"`
	CancelFunc context.CancelFunc `json:"-"`
}

type TaskMessage struct {
	From      string    `json:"from"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type SubAgentType string

const (
	SubAgentGeneral      SubAgentType = "general-purpose"
	SubAgentExplore      SubAgentType = "Explore"
	SubAgentPlan         SubAgentType = "Plan"
	SubAgentVerification SubAgentType = "Verification"
)
