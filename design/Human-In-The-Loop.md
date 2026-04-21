# openharness-go HITL (Human-In-The-Loop) 实现

## 三方案对比分析

| 维度       | claw-code (Rust)             | OpenHarness (Python)       | openharness-go               |
| ---------- | ---------------------------- | -------------------------- | ---------------------------- |
| 阻塞机制   | raw stdin/stdout             | asyncio.Future + WebSocket | Go channel per-request       |
| 多前端支持 | 仅 CLI                       | WebSocket + HTTP           | CLI / JSON-Lines / HTTP 均可 |
| 多选题支持 | options: Option<Vec<String>> | 无（仅自由文本）           | 完整支持                     |
| 权限请求   | 硬编码 stdin                 | Future + modal_request     | 统一 Manager.AskPermission   |
| 取消支持   | 无                           | Future.cancel()            | context.Context 原生         |
| 并发安全   | 单线程                       | asyncio 单线程             | sync.Mutex + channel         |

## 架构总览

```
┌──────────────────┐   AskUser(ctx, question, options)   ┌───────────────────┐
│  AskUserQuestion │ ──────────────────────────────────► │                   │
│  Tool            │                                      │   HITL Manager    │
│  (pkg/tools/     │ ◄────────────────────────────────── │   (pkg/hitl/)     │
│   builtin/)      │   answer (via Go channel)            │                   │
└──────────────────┘                                      └────────┬──────────┘
                                                                   │
                                                    BackendEvent   │  FrontendRequest
                                                   (modal_request) │  (question_response)
                                                                   ▼
                                                     ┌──────────────────────────┐
                                                     │    Frontend Adapter      │
                                                     │  ┌──────┐ ┌──────────┐  │
                                                     │  │ CLI  │ │JSON-Lines│  │
                                                     │  │stdin │ │ protocol │  │
                                                     │  └──────┘ └──────────┘  │
                                                     └──────────────────────────┘
```

## 新增 / 修改的文件（共 11 个）

### 新增 4 个文件：

| 文件                          | 行数 | 职责                                                                                     |
| ----------------------------- | ---- | ---------------------------------------------------------------------------------------- |
| pkg/hitl/types.go             | 123  | HITL 协议类型（BackendEvent, FrontendRequest, ModalInfo），无依赖避免循环引用            |
| pkg/hitl/manager.go           | 175  | 核心 Manager：per-request channel 跟踪，AskQuestion/AskPermission 阻塞等待，Resolve 派发 |
| pkg/hitl/cli_adapter.go       | 122  | CLI 前端适配器：带格式化的 stdin/stdout 交互，支持多选题数字输入                         |
| pkg/hitl/jsonlines_adapter.go | 116  | JSON-Lines 前端适配器：TUI/IDE 用，双向 JSON 行协议                                      |

### 修改 4 个文件 + 1 个新工具：

| 文件                                   | 变更                                                                                          |
| -------------------------------------- | --------------------------------------------------------------------------------------------- |
| pkg/tools/base.go                      | +AskUserFunc/AskPermissionFunc 回调类型，ToolExecutionContext 新增 AskUser/AskPermission 字段 |
| pkg/tools/builtin/ask_user_question.go | 新增 101 行，完整的 AskUserQuestionTool（自由文本 + 多选题）                                  |
| pkg/tools/builtin/register.go          | 注册 NewAskUserQuestionTool()                                                                 |
| pkg/engine/query.go                    | QueryContext 新增 HITL 回调字段，executeToolCall() 注入到 ToolExecutionContext                |
| pkg/engine/query_engine.go             | 新增 WithAskUser/WithAskPermission 选项，SubmitMessage 传递到 QueryContext                    |
| pkg/ui/runtime.go                      | 新增 WithHITLCallbacks RuntimeOption，BuildRuntime 透传到 QueryEngine                         |
| pkg/ui/app.go                          | RunREPL 注入 CLIAdapter，新增 RunJSONLinesMode 完整的结构化协议会话                           |

## 核心设计决策

1. Go channel 替代 asyncio.Future
   Python 的 `asyncio.Future` 是单次写入、单次读取的 promise。Go 的 `chan T` with buffer=1 是天然等价物：

```go
// Manager 内部
ch := make(chan string, 1)   // ← 等价于 Future[str]
m.questionReqs[reqID] = ch   // ← 注册等待者

// 工具侧阻塞等待
select {
case answer := <-ch:         // ← await future
    return answer, nil
case <-ctx.Done():           // ← 原生取消，Python 需要额外的 Future.cancel()
    return "", ctx.Err()
}

// 前端侧解决
ch <- answer                 // ← future.set_result(answer)
```

2.  前端无关的回调注入
    HITL 回调以函数类型 `AskUserFunc` 定义在 `pkg/tools/` 中（最底层包），通过依赖注入从上层穿透到工具：

    ```
    RuntimeOption → QueryEngineOption → QueryContext → ToolExecutionContext → Tool.Execute()
    ```

    这意味着：
    - **REPL 模式**：直接注入 `CLIAdapter.AskUser`（stdin/stdout 交互）
    - **JSON-Lines 模式**：注入 `Manager.AskQuestion`（通过协议与远程 TUI 交互）
    - **HTTP/WebSocket 模式**：同样注入 `Manager.AskQuestion`，emit 到 WebSocket
    - **非交互模式**：不注入（`nil`），工具优雅返回 "unavailable in this session"

3.  多选题交互
    CLI 模式下的多选题渲染：

    ```
    ╭─ Question from assistant ─────────────────────────
    │ Which test framework do you prefer?
    │
    │ [1] Jest
    │ [2] Vitest
    │ [3] Mocha
    ╰──────────────────────────────────────────────────
    Enter choice (1-3) or type your answer:
    ```

    用户输入数字自动映射到选项值，也支持直接输入自由文本（兼容两种模式）。

4.  JSON-Lines 协议示例

    ```json
    // Backend → Frontend (modal_request)
    {"type":"modal_request","modal":{"kind":"question","request_id":"hitl_1","question":"Which framework?","options":["React","Vue","Svelte"]}}

    // Frontend → Backend (question_response)
    {"type":"question_response","request_id":"hitl_1","answer":"React"}
    ```

5.  权限请求复用
    权限请求和问题请求共用同一套 Manager 机制，只是 channel 类型不同（`chan bool` vs `chan string`），协议中通过 `ModalKind` 区分 `"question"` / `"permission"`，前端据此渲染不同 UI。

## 附代码变更参考

### 新增文件

1. `pkg/protocol/types.go` — HITL 协议类型（独立包，避免循环引用）

```go
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
```

2. `pkg/hitl/manager.go` — 核心 HITL Manager（per-request channel 模式）

```go
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
```

3. `pkg/hitl/cli_adapter.go` — CLI 前端适配器（REPL 模式）

```go
package hitl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// CLIAdapter handles HITL interactions via stdin/stdout.
type CLIAdapter struct {
	reader io.Reader
	writer io.Writer
}

func NewCLIAdapter(r io.Reader, w io.Writer) *CLIAdapter {
	return &CLIAdapter{reader: r, writer: w}
}

// AskUser implements tools.AskUserFunc for CLI environments.
func (a *CLIAdapter) AskUser(ctx context.Context, question string, options []string) (string, error) {
	fmt.Fprintf(a.writer, "\n╭─ Question from assistant ─────────────────────────\n")
	fmt.Fprintf(a.writer, "│ %s\n", question)

	if len(options) > 0 {
		fmt.Fprintf(a.writer, "│\n")
		for i, opt := range options {
			fmt.Fprintf(a.writer, "│  [%d] %s\n", i+1, opt)
		}
		fmt.Fprintf(a.writer, "╰──────────────────────────────────────────────────\n")
		fmt.Fprintf(a.writer, "Enter choice (1-%d) or type your answer: ", len(options))
	} else {
		fmt.Fprintf(a.writer, "╰──────────────────────────────────────────────────\n")
		fmt.Fprintf(a.writer, "Your answer: ")
	}

	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(a.reader)
		if scanner.Scan() {
			lineCh <- scanner.Text()
		} else {
			if err := scanner.Err(); err != nil {
				errCh <- err
			} else {
				errCh <- io.EOF
			}
		}
	}()

	select {
	case line := <-lineCh:
		answer := strings.TrimSpace(line)
		if len(options) > 0 {
			if idx, err := strconv.Atoi(answer); err == nil && idx >= 1 && idx <= len(options) {
				return options[idx-1], nil
			}
		}
		return answer, nil
	case err := <-errCh:
		return "", fmt.Errorf("failed to read user input: %w", err)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// AskPermission implements tools.AskPermissionFunc for CLI environments.
func (a *CLIAdapter) AskPermission(ctx context.Context, toolName, reason string) (bool, error) {
	fmt.Fprintf(a.writer, "\n╭─ Permission request ──────────────────────────────\n")
	fmt.Fprintf(a.writer, "│ Tool: %s\n", toolName)
	fmt.Fprintf(a.writer, "│ Reason: %s\n", reason)
	fmt.Fprintf(a.writer, "╰──────────────────────────────────────────────────\n")
	fmt.Fprintf(a.writer, "Allow? (y/n): ")

	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(a.reader)
		if scanner.Scan() {
			lineCh <- scanner.Text()
		} else {
			if err := scanner.Err(); err != nil {
				errCh <- err
			} else {
				errCh <- io.EOF
			}
		}
	}()

	select {
	case line := <-lineCh:
		answer := strings.TrimSpace(strings.ToLower(line))
		return answer == "y" || answer == "yes", nil
	case err := <-errCh:
		return false, fmt.Errorf("failed to read user input: %w", err)
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
```

4. `pkg/hitl/jsonlines_adapter.go` — JSON-Lines 前端适配器（TUI/IDE/ 远程）

```go
package hitl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/openharness/openharness/pkg/protocol"
)

// JSONLinesAdapter handles HITL via JSON-Lines protocol over io.Reader/Writer.
type JSONLinesAdapter struct {
	reader     io.Reader
	writer     io.Writer
	manager    *Manager
	scanOnce   sync.Once
	scanner    *bufio.Scanner
	incomingCh chan *protocol.FrontendRequest
}

func NewJSONLinesAdapter(r io.Reader, w io.Writer) *JSONLinesAdapter {
	return &JSONLinesAdapter{
		reader:     r,
		writer:     w,
		incomingCh: make(chan *protocol.FrontendRequest, 32),
	}
}

func (a *JSONLinesAdapter) SetManager(m *Manager) { a.manager = m }

func (a *JSONLinesAdapter) StartReadLoop(ctx context.Context) error {
	a.scanOnce.Do(func() {
		a.scanner = bufio.NewScanner(a.reader)
		a.scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !a.scanner.Scan() {
			if err := a.scanner.Err(); err != nil {
				return fmt.Errorf("jsonlines read error: %w", err)
			}
			return io.EOF
		}
		line := a.scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		req, err := protocol.ParseFrontendRequest(line)
		if err != nil {
			a.emit(&protocol.BackendEvent{
				Type:  protocol.BEError,
				Error: fmt.Sprintf("invalid request: %v", err),
			})
			continue
		}
		switch req.Type {
		case protocol.FRQuestionResponse, protocol.FRPermissionResponse:
			if a.manager != nil {
				if routeErr := a.manager.HandleFrontendRequest(req); routeErr != nil {
					a.emit(&protocol.BackendEvent{
						Type:  protocol.BEError,
						Error: routeErr.Error(),
					})
				}
			}
		default:
			select {
			case a.incomingCh <- req:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (a *JSONLinesAdapter) emit(event *protocol.BackendEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintf(a.writer, "%s\n", data)
}

func (a *JSONLinesAdapter) EmitFn() func(event *protocol.BackendEvent) { return a.emit }

func (a *JSONLinesAdapter) IncomingRequests() <-chan *protocol.FrontendRequest {
	return a.incomingCh
}
```

5. `pkg/tools/builtin/ask_user_question.go` — AskUserQuestion 工具

```go
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openharness/openharness/pkg/tools"
)

type askUserQuestionTool struct {
	tools.BaseToolHelper
}

type askUserInput struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

func NewAskUserQuestionTool() tools.BaseTool {
	return &askUserQuestionTool{
		BaseToolHelper: tools.BaseToolHelper{
			ToolName: "ask_user_question",
			ToolDescription: `Ask the user a question and wait for their response. Use this tool when you need clarification, confirmation, or input from the user to proceed with a task.

You can ask:
- Free-form questions: The user types a text answer.
- Multiple-choice questions: Provide options and the user picks one.

Guidelines:
- Only ask when you genuinely need user input to proceed.
- Be specific and concise in your questions.
- For multiple-choice, provide clear and distinct options.
- Do not use this tool for rhetorical questions.`,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question": map[string]any{
						"type":        "string",
						"description": "The question to ask the user.",
					},
					"options": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional list of choices for a multiple-choice question.",
					},
				},
				"required": []string{"question"},
			},
			ReadOnly: true,
		},
	}
}

func (t *askUserQuestionTool) Execute(ctx context.Context, input json.RawMessage, execCtx *tools.ToolExecutionContext) (*tools.ToolResult, error) {
	var params askUserInput
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.NewToolResultError(fmt.Sprintf("invalid input: %v", err)), nil
	}
	if strings.TrimSpace(params.Question) == "" {
		return tools.NewToolResultError("question must not be empty"), nil
	}
	if execCtx.AskUser == nil {
		return tools.NewToolResultError(
			"ask_user_question is unavailable in this session (no interactive frontend connected)",
		), nil
	}

	answer, err := execCtx.AskUser(ctx, params.Question, params.Options)
	if err != nil {
		return tools.NewToolResultError(fmt.Sprintf("failed to get user response: %v", err)), nil
	}

	answer = strings.TrimSpace(answer)
	if answer == "" {
		return tools.NewToolResult("(no response from user)"), nil
	}
	return tools.NewToolResult(answer), nil
}
```

## 修改文件（diff 关键部分）

6. `pkg/tools/base.go` — 新增 HITL 回调类型 + ToolExecutionContext 扩展

```go
+ // AskUserFunc is the callback signature for asking the user a question.
+ type AskUserFunc func(ctx context.Context, question string, options []string) (string, error)
+
+ // AskPermissionFunc is the callback for requesting permission to execute a tool.
+ type AskPermissionFunc func(ctx context.Context, toolName string, reason string) (bool, error)

  type ToolExecutionContext struct {
-     Cwd      string         `json:"cwd"`
-     Metadata map[string]any `json:"metadata,omitempty"`
+     Cwd           string            `json:"cwd"`
+     Metadata      map[string]any    `json:"metadata,omitempty"`
+     AskUser       AskUserFunc       `json:"-"`
+     AskPermission AskPermissionFunc `json:"-"`
  }
```

7. `pkg/tools/builtin/register.go` — 注册新工具

```go
  builtins := []tools.BaseTool{
      NewBashTool(),
      NewFileReadTool(),
      NewFileWriteTool(),
      NewFileEditTool(),
      NewGlobTool(),
      NewGrepTool(),
+     NewAskUserQuestionTool(),
  }
```

8. `pkg/engine/query.go` — QueryContext 新增 HITL 字段 + executeToolCall 注入

```go
  type QueryContext struct {
      // ... existing fields ...
+     AskUser       tools.AskUserFunc
+     AskPermission tools.AskPermissionFunc
  }

  func executeToolCall(...) ToolResultBlock {
      // ... existing logic ...
      execCtx := tools.NewToolExecutionContext(qctx.Cwd)
+     execCtx.AskUser = qctx.AskUser
+     execCtx.AskPermission = qctx.AskPermission
      result, err := tool.Execute(ctx, toolInput, execCtx)
      // ...
  }
```

9. `pkg/engine/query_engine.go` — 新增 WithAskUser/WithAskPermission 选项

```go
  type QueryEngine struct {
      // ... existing fields ...
+     askUser       tools.AskUserFunc
+     askPermission tools.AskPermissionFunc
  }

+ func WithAskUser(fn tools.AskUserFunc) QueryEngineOption {
+     return func(qe *QueryEngine) { qe.askUser = fn }
+ }
+
+ func WithAskPermission(fn tools.AskPermissionFunc) QueryEngineOption {
+     return func(qe *QueryEngine) { qe.askPermission = fn }
+ }

  func (qe *QueryEngine) SubmitMessage(...) {
      qctx := &QueryContext{
          // ... existing fields ...
+         AskUser:       qe.askUser,
+         AskPermission: qe.askPermission,
      }
  }
```

10. `pkg/ui/runtime.go` — 新增 WithHITLCallbacks RuntimeOption

```go
+ type RuntimeOption func(*runtimeConfig)
+
+ type runtimeConfig struct {
+     askUser       tools.AskUserFunc
+     askPermission tools.AskPermissionFunc
+ }
+
+ func WithHITLCallbacks(askUser tools.AskUserFunc, askPermission tools.AskPermissionFunc) RuntimeOption {
+     return func(cfg *runtimeConfig) {
+         cfg.askUser = askUser
+         cfg.askPermission = askPermission
+     }
+ }

- func BuildRuntime(settings *config.Settings, cwd string) (*RuntimeBundle, error) {
+ func BuildRuntime(settings *config.Settings, cwd string, opts ...RuntimeOption) (*RuntimeBundle, error) {
+     var cfg runtimeConfig
+     for _, o := range opts { o(&cfg) }
      // ...
+     var engineOpts []engine.QueryEngineOption
+     if cfg.askUser != nil {
+         engineOpts = append(engineOpts, engine.WithAskUser(cfg.askUser))
+     }
+     if cfg.askPermission != nil {
+         engineOpts = append(engineOpts, engine.WithAskPermission(cfg.askPermission))
+     }
-     qe := engine.NewQueryEngine(adapter, toolReg, cwd, ...)
+     qe := engine.NewQueryEngine(adapter, toolReg, cwd, ..., engineOpts...)
```

11. `pkg/ui/app.go` — RunREPL 注入 CLIAdapter + 新增 RunJSONLinesMode

```go
+ import "github.com/openharness/openharness/pkg/hitl"
+ import "github.com/openharness/openharness/pkg/protocol"

  func RunREPL(ctx context.Context, settings *config.Settings) error {
      // ...
+     cliAdapter := hitl.NewCLIAdapter(os.Stdin, os.Stdout)
-     rt, err := BuildRuntime(settings, cwd)
+     rt, err := BuildRuntime(settings, cwd,
+         WithHITLCallbacks(cliAdapter.AskUser, cliAdapter.AskPermission),
+     )
      // ...
  }

+ // RunJSONLinesMode — full JSON-Lines protocol session for TUI/IDE/remote.
+ func RunJSONLinesMode(ctx context.Context, settings *config.Settings) error {
+     // creates JSONLinesAdapter → Manager → wires AskQuestion/AskPermission
+     // starts read loop goroutine, emits ready event, processes submit_line
+     // ...
+ }
```
