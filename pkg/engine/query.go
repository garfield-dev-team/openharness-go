// Package engine implements the core agent loop that orchestrates LLM calls
// and tool execution.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/openharness/openharness/pkg/tools"
	"github.com/openharness/openharness/pkg/types"
)

// ---------------------------------------------------------------------------
// Stream event types – emitted by RunQuery.
// ---------------------------------------------------------------------------

// EventType enumerates the kinds of events that RunQuery can yield.
type EventType string

const (
	EventTextDelta              EventType = "text_delta"
	EventAssistantTurnComplete  EventType = "assistant_turn_complete"
	EventToolExecutionStarted   EventType = "tool_execution_started"
	EventToolExecutionCompleted EventType = "tool_execution_completed"
	EventError                  EventType = "error"
)

// StreamEvent is a single event emitted during a query.
type StreamEvent struct {
	Type       EventType         `json:"type"`
	Text       string            `json:"text,omitempty"`
	ToolName   string            `json:"tool_name,omitempty"`
	ToolUseID  string            `json:"tool_use_id,omitempty"`
	ToolResult *tools.ToolResult `json:"tool_result,omitempty"`
	Error      error             `json:"-"`
}

// StreamEventWithUsage pairs a StreamEvent with an optional usage snapshot.
type StreamEventWithUsage struct {
	Event StreamEvent
	Usage *types.UsageSnapshot
}

// ---------------------------------------------------------------------------
// ToolUse represents a tool invocation requested by the assistant.
// ---------------------------------------------------------------------------

// ToolUse holds data extracted from a tool_use content block.
type ToolUse struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResultBlock is the outcome of a single tool invocation.
type ToolResultBlock struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ---------------------------------------------------------------------------
// Permission checking.
// ---------------------------------------------------------------------------

// PermissionChecker decides whether a tool invocation is allowed.
type PermissionChecker interface {
	Check(ctx context.Context, toolName string, input json.RawMessage) error
}

// AllowAllPermissions is a PermissionChecker that permits everything.
type AllowAllPermissions struct{}

func (AllowAllPermissions) Check(context.Context, string, json.RawMessage) error { return nil }

// ---------------------------------------------------------------------------
// Hook executor.
// ---------------------------------------------------------------------------

// HookExecutor is called before/after tool invocations.
type HookExecutor interface {
	PreToolUse(ctx context.Context, toolName string, input json.RawMessage) error
	PostToolUse(ctx context.Context, toolName string, result *tools.ToolResult) error
}

// ---------------------------------------------------------------------------
// LLM client abstraction.
// ---------------------------------------------------------------------------

// LLMStreamEvent is a single token/event from the LLM streaming response.
type LLMStreamEvent struct {
	TextDelta string
	Message   *types.ConversationMessage
	Usage     *types.UsageSnapshot
	Err       error
}

// StreamingLLMClient abstracts the LLM API.
type StreamingLLMClient interface {
	StreamMessage(ctx context.Context, params LLMRequestParams) (<-chan LLMStreamEvent, error)
}

// LLMRequestParams contains everything needed for an LLM call.
type LLMRequestParams struct {
	Model        string                       `json:"model"`
	SystemPrompt string                       `json:"system_prompt"`
	Messages     []types.ConversationMessage  `json:"messages"`
	Tools        []map[string]any             `json:"tools,omitempty"`
	MaxTokens    int                          `json:"max_tokens"`
}

// ---------------------------------------------------------------------------
// QueryContext – everything RunQuery needs.
// ---------------------------------------------------------------------------

// QueryContext bundles the dependencies for a single query execution.
type QueryContext struct {
	APIClient         StreamingLLMClient
	ToolRegistry      *tools.ToolRegistry
	PermissionChecker PermissionChecker
	Cwd               string
	Model             string
	SystemPrompt      string
	MaxTokens         int
	MaxTurns          int
	HookExecutor      HookExecutor
}

// ---------------------------------------------------------------------------
// RunQuery – the core agent loop.
// ---------------------------------------------------------------------------

// RunQuery executes the multi-turn agent loop.
func RunQuery(ctx context.Context, qctx *QueryContext, messages []types.ConversationMessage) <-chan StreamEventWithUsage {
	ch := make(chan StreamEventWithUsage, 64)

	maxTurns := qctx.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 8
	}

	go func() {
		defer close(ch)

		for turn := 0; turn < maxTurns; turn++ {
			params := LLMRequestParams{
				Model:        qctx.Model,
				SystemPrompt: qctx.SystemPrompt,
				Messages:     messages,
				Tools:        qctx.ToolRegistry.ToAPISchema(),
				MaxTokens:    qctx.MaxTokens,
			}

			streamCh, err := qctx.APIClient.StreamMessage(ctx, params)
			if err != nil {
				ch <- StreamEventWithUsage{Event: StreamEvent{Type: EventError, Error: err}}
				return
			}

			var assistantMsg *types.ConversationMessage
			var usage *types.UsageSnapshot

			for ev := range streamCh {
				if ev.Err != nil {
					ch <- StreamEventWithUsage{Event: StreamEvent{Type: EventError, Error: ev.Err}}
					return
				}
				if ev.TextDelta != "" {
					ch <- StreamEventWithUsage{
						Event: StreamEvent{Type: EventTextDelta, Text: ev.TextDelta},
					}
				}
				if ev.Message != nil {
					assistantMsg = ev.Message
				}
				if ev.Usage != nil {
					usage = ev.Usage
				}
			}

			if assistantMsg == nil {
				ch <- StreamEventWithUsage{Event: StreamEvent{
					Type:  EventError,
					Error: fmt.Errorf("LLM stream ended without a complete message"),
				}}
				return
			}

			messages = append(messages, *assistantMsg)

			ch <- StreamEventWithUsage{
				Event: StreamEvent{Type: EventAssistantTurnComplete},
				Usage: usage,
			}

			// Collect tool uses.
			var toolUses []ToolUse
			for _, block := range assistantMsg.Content {
				if block.Type == "tool_use" {
					inputBytes, _ := json.Marshal(block.Input)
					toolUses = append(toolUses, ToolUse{
						ID:    block.ID,
						Name:  block.Name,
						Input: inputBytes,
					})
				}
			}
			if len(toolUses) == 0 {
				return // done
			}

			// Execute all tool calls concurrently.
			resultBlocks := make([]ToolResultBlock, len(toolUses))
			var wg sync.WaitGroup
			wg.Add(len(toolUses))

			for i, tu := range toolUses {
				go func(idx int, tu ToolUse) {
					defer wg.Done()

					ch <- StreamEventWithUsage{Event: StreamEvent{
						Type:      EventToolExecutionStarted,
						ToolName:  tu.Name,
						ToolUseID: tu.ID,
					}}

					rb := executeToolCall(ctx, qctx, tu.Name, tu.ID, tu.Input)
					resultBlocks[idx] = rb

					ch <- StreamEventWithUsage{Event: StreamEvent{
						Type:       EventToolExecutionCompleted,
						ToolName:   tu.Name,
						ToolUseID:  tu.ID,
						ToolResult: &tools.ToolResult{Output: rb.Content, IsError: rb.IsError},
					}}
				}(i, tu)
			}
			wg.Wait()

			// Build the tool-results user message.
			var toolResultContents []types.ContentBlock
			for _, rb := range resultBlocks {
				toolResultContents = append(toolResultContents, types.NewToolResultBlock(
					rb.ToolUseID, rb.Content, rb.IsError,
				))
			}
			messages = append(messages, types.ConversationMessage{
				Role:    "user",
				Content: toolResultContents,
			})
		}

		ch <- StreamEventWithUsage{Event: StreamEvent{
			Type:  EventError,
			Error: fmt.Errorf("exceeded maximum turn limit (%d)", maxTurns),
		}}
	}()

	return ch
}

// ---------------------------------------------------------------------------
// executeToolCall
// ---------------------------------------------------------------------------

func executeToolCall(ctx context.Context, qctx *QueryContext, toolName, toolUseID string, toolInput json.RawMessage) ToolResultBlock {
	fail := func(msg string) ToolResultBlock {
		return ToolResultBlock{ToolUseID: toolUseID, Content: msg, IsError: true}
	}

	if qctx.HookExecutor != nil {
		if err := qctx.HookExecutor.PreToolUse(ctx, toolName, toolInput); err != nil {
			return fail(fmt.Sprintf("pre_tool_use hook error: %v", err))
		}
	}

	tool := qctx.ToolRegistry.Get(toolName)
	if tool == nil {
		return fail(fmt.Sprintf("unknown tool: %s", toolName))
	}

	if qctx.PermissionChecker != nil {
		if err := qctx.PermissionChecker.Check(ctx, toolName, toolInput); err != nil {
			return fail(fmt.Sprintf("permission denied: %v", err))
		}
	}

	execCtx := tools.NewToolExecutionContext(qctx.Cwd)
	result, err := tool.Execute(ctx, toolInput, execCtx)
	if err != nil {
		return fail(fmt.Sprintf("tool execution error: %v", err))
	}

	if qctx.HookExecutor != nil {
		_ = qctx.HookExecutor.PostToolUse(ctx, toolName, result)
	}

	return ToolResultBlock{
		ToolUseID: toolUseID,
		Content:   result.Output,
		IsError:   result.IsError,
	}
}

// nowMillis returns the current time in milliseconds.
func nowMillis() int64 {
	return time.Now().UnixMilli()
}
