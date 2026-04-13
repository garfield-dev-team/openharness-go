# Task 系统核心设计

Claude Code 正在用 Task 替代 Todo，核心理念转变：

```
旧模式 (TodoWrite):   Model → 写一个扁平的待办列表 → 自己串行执行
新模式 (Task):        Model → 批量创建 Task → 每个 Task 启动独立子 Agent → 并行执行 → 汇总结果
```

## claw-code 的 Task 架构（3 层）

```
┌─────────────────────────────────────────────────────┐
│ Layer 1: TaskPacket (结构化任务契约) │
│ ┌─────────────────────────────────────────────────┐ │
│ │ objective: "实现XX功能" │ │
│ │ scope: ["src/foo.rs", "tests/"] │ │
│ │ repo / branch_policy / commit_policy │ │
│ │ acceptance_tests: ["cargo test --lib"] │ │
│ │ reporting_contract: "JSON summary" │ │
│ │ escalation_policy: "stop on 3 failures" │ │
│ └─────────────────────────────────────────────────┘ │
├─────────────────────────────────────────────────────┤
│ Layer 2: TaskRegistry (内存注册表) │
│ ┌─────────────────────────────────────────────────┐ │
│ │ Arc<Mutex<HashMap<task_id, TaskEntry>>> │ │
│ │ TaskEntry { │ │
│ │ id, prompt, status, output, messages, │ │
│ │ agent_id, team_id, created_at, updated_at │ │
│ │ } │ │
│ │ Status: Created → Running → Completed/Failed/Stopped│ │
│ │ │ │
│ │ API: create / get / list / stop / update / │ │
│ │ output / append_output / set_status / │ │
│ │ assign_team / send_message │ │
│ └─────────────────────────────────────────────────┘ │
├─────────────────────────────────────────────────────┤
│ Layer 3: Agent Executor (子Agent执行器) │
│ ┌─────────────────────────────────────────────────┐ │
│ │ execute_agent() → spawn_agent_job() [新goroutine] │ │
│ │ → build SubagentToolExecutor (工具白名单沙箱) │ │
│ │ → build ConversationRuntime │ │
│ │ → run_turn() 最多32轮 │ │
│ │ → 写结果到 .claw/agents/{id}.json │ │
│ └─────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────┘
```

## SubAgent 类型与工具白名单

| 子 Agent 类型     | 允许的工具                           | 用途     |
| ----------------- | ------------------------------------ | -------- |
| `general-purpose` | 几乎全部（除 Agent 自身）            | 通用任务 |
| `Explore`         | 只读工具（Read/Glob/Grep/Bash 只读） | 代码探索 |
| `Plan`            | 只读 + TodoWrite                     | 架构规划 |
| `Verification`    | 只读 + Bash                          | 测试验证 |
| `claw-guide`      | 只读 + SendUserMessage               | 引导交互 |

## openharness-go 支持 Task 系统的实施方案

### Phase 1: 核心数据结构

```go
// pkg/tasks/types.go

// TaskStatus 任务生命周期状态
type TaskStatus string

const (
    TaskCreated   TaskStatus = "created"
    TaskRunning   TaskStatus = "running"
    TaskCompleted TaskStatus = "completed"
    TaskFailed    TaskStatus = "failed"
    TaskStopped   TaskStatus = "stopped"
)

// TaskPacket 结构化任务契约（对标 claw-code task_packet.rs）
type TaskPacket struct {
    Objective        string            `json:"objective"`
    Scope            []string          `json:"scope"`
    Repo             string            `json:"repo,omitempty"`
    BranchPolicy     string            `json:"branch_policy,omitempty"`     // "auto_create" | "current" | "specific:<name>"
    AcceptanceTests  []string          `json:"acceptance_tests,omitempty"`
    CommitPolicy     string            `json:"commit_policy,omitempty"`     // "auto_commit" | "stage_only" | "none"
    ReportingContract string           `json:"reporting_contract,omitempty"`
    EscalationPolicy string            `json:"escalation_policy,omitempty"`
}

// TaskEntry 注册表中的任务条目
type TaskEntry struct {
    ID         string       `json:"id"`
    Prompt     string       `json:"prompt"`
    Status     TaskStatus   `json:"status"`
    AgentID    string       `json:"agent_id,omitempty"`
    AgentType  string       `json:"agent_type,omitempty"`
    TeamID     string       `json:"team_id,omitempty"`
    Output     []string     `json:"output"`
    Messages   []TaskMessage `json:"messages"`
    Packet     *TaskPacket  `json:"packet,omitempty"`
    CreatedAt  time.Time    `json:"created_at"`
    UpdatedAt  time.Time    `json:"updated_at"`
    Error      string       `json:"error,omitempty"`
    CancelFunc context.CancelFunc `json:"-"`
}

// TaskMessage Agent间的消息传递
type TaskMessage struct {
    From      string    `json:"from"`
    Content   string    `json:"content"`
    Timestamp time.Time `json:"timestamp"`
}
```

### Phase 2: TaskRegistry（线程安全注册表）

```go
// pkg/tasks/registry.go

type TaskRegistry struct {
    mu      sync.RWMutex
    tasks   map[string]*TaskEntry
    counter atomic.Int64
}

func NewTaskRegistry() *TaskRegistry

// CRUD
func (r *TaskRegistry) Create(prompt string, agentType string, packet *TaskPacket) (string, error)
func (r *TaskRegistry) Get(id string) (*TaskEntry, error)
func (r *TaskRegistry) List() []*TaskEntry
func (r *TaskRegistry) Stop(id string) error
func (r *TaskRegistry) Update(id string, status TaskStatus) error
func (r *TaskRegistry) Remove(id string) error

// 输出与消息
func (r *TaskRegistry) AppendOutput(id string, line string) error
func (r *TaskRegistry) GetOutput(id string) (string, error)
func (r *TaskRegistry) SendMessage(id string, msg TaskMessage) error
func (r *TaskRegistry) GetMessages(id string) ([]TaskMessage, error)

// Team关联
func (r *TaskRegistry) AssignTeam(id string, teamID string) error

// ID生成: "task_{hex_timestamp}_{counter}"
func (r *TaskRegistry) generateID() string
```

### Phase 3: SubAgent 执行器

```go
// pkg/tasks/executor.go

// SubAgentType 子Agent类型枚举
type SubAgentType string

const (
    SubAgentGeneral      SubAgentType = "general-purpose"
    SubAgentExplore      SubAgentType = "Explore"
    SubAgentPlan         SubAgentType = "Plan"
    SubAgentVerification SubAgentType = "Verification"
)

// SubAgentConfig 子Agent配置
type SubAgentConfig struct {
    Type        SubAgentType
    MaxTurns    int      // 默认32
    Prompt      string
    SystemPrompt string
    Isolation   string   // "none" | "worktree"
    AllowedTools []string // nil = use type default whitelist
}

// SubAgentExecutor 子Agent执行器（实现工具白名单沙箱）
type SubAgentExecutor struct {
    registry     *TaskRegistry
    toolRegistry *tools.ToolRegistry
    apiClient    api.MessageStreamer
    baseConfig   *config.Settings
}

// SpawnAgent 启动子Agent（在新goroutine中运行）
func (e *SubAgentExecutor) SpawnAgent(ctx context.Context, cfg SubAgentConfig) (agentID string, err error) {
    // 1. 在 TaskRegistry 中创建 task entry
    // 2. 构建受限工具集（白名单沙箱）
    // 3. 启动 goroutine:
    //    a. 构建 sandboxed ToolRegistry
    //    b. 构建 system prompt（含任务上下文）
    //    c. 调用 engine.RunQuery() 执行，最多 maxTurns 轮
    //    d. 将结果写回 TaskRegistry
    //    e. 可选：写 .openharness/agents/{id}.json
}

// buildSandboxedRegistry 根据子Agent类型构建受限工具注册表
func (e *SubAgentExecutor) buildSandboxedRegistry(agentType SubAgentType) *tools.ToolRegistry {
    allowed := getToolWhitelist(agentType)
    sandbox := tools.NewToolRegistry()
    for _, name := range allowed {
        if tool := e.toolRegistry.Get(name); tool != nil {
            sandbox.Register(tool)
        }
    }
    return sandbox
}

// getToolWhitelist 返回各子Agent类型的工具白名单
func getToolWhitelist(t SubAgentType) []string {
    switch t {
    case SubAgentExplore:
        return []string{"Read", "Glob", "Grep", "Bash", "LS"} // Bash限只读
    case SubAgentPlan:
        return []string{"Read", "Glob", "Grep", "Bash", "TodoWrite"}
    case SubAgentVerification:
        return []string{"Read", "Glob", "Grep", "Bash"}
    default: // general-purpose
        return nil // 允许所有工具（除Agent自身，防递归）
    }
}
```

### Phase 4: 9 个 Task 工具

```go
// pkg/tools/builtin/task_*.go

TaskCreateTool      // 创建任务 → registry.Create() + executor.SpawnAgent()
TaskGetTool         // 查询任务状态 → registry.Get()
TaskListTool        // 列出所有任务 → registry.List()
TaskStopTool        // 停止任务 → registry.Stop() → cancel context
TaskUpdateTool      // 更新任务状态 → registry.Update()
TaskOutputTool      // 获取任务输出 → registry.GetOutput()
TaskSendMessageTool // 向任务发消息 → registry.SendMessage()
TaskPacketCreateTool  // 创建结构化TaskPacket
TaskPacketValidateTool // 验证TaskPacket完整性
```

### Phase 5: Agent 工具（核心集成点）

```go
// pkg/tools/builtin/agent.go

// AgentTool 创建子Agent的工具（被LLM调用）
type AgentTool struct {
    executor *SubAgentExecutor
}

// Execute 参数: {prompt, description, subagent_type, model?, isolation?}
func (t *AgentTool) Execute(args AgentInput, ctx ToolExecutionContext) ToolResult {
    // 1. 解析 subagent_type（默认 general-purpose）
    // 2. 构建 SubAgentConfig
    // 3. 调用 executor.SpawnAgent() → 返回 agent_id
    // 4. 异步模式：立即返回 agent_id，LLM后续用 TaskGet 轮询
    //    同步模式：等待完成后返回结果
}
```

### Phase 6: 核心执行流程（并行 Task 场景）

```
用户: "帮我重构这3个模块"
   │
   ▼
主Agent (engine.RunQuery)
   │
   ├──→ LLM 返回 3 个 tool_use: Agent(重构模块A), Agent(重构模块B), Agent(重构模块C)
   │
   ├──→ 并发执行 3 个 Agent Tool:
   │     ├── goroutine-1: SpawnAgent("重构模块A", type=general)
   │     │     ├── TaskRegistry.Create() → task_001
   │     │     ├── buildSandboxedRegistry(general)
   │     │     └── engine.RunQuery(sandboxed, maxTurns=32) → 结果写回 registry
   │     │
   │     ├── goroutine-2: SpawnAgent("重构模块B", type=general)
   │     │     └── ... 同上 → task_002
   │     │
   │     └── goroutine-3: SpawnAgent("重构模块C", type=general)
   │           └── ... 同上 → task_003
   │
   ├──→ 等待所有子Agent完成（或超时）
   │
   ├──→ 收集结果，组装 tool_result 返回给 LLM
   │
   └──→ LLM 生成总结回复给用户
```

### Phase 7: engine.RunQuery 改造点

```go
// 当前 RunQuery 已支持并发工具执行（WaitGroup），需要增加：

// 1. Agent工具的特殊处理：检测是否为Agent类型工具
if tool.Name() == "Agent" || tool.Name() == "Task" {
    // 异步模式：SpawnAgent后立即返回task_id
    // 主循环下一轮可以通过TaskGet获取状态
}

// 2. 自动压缩：在每轮开始时检查token数
if estimateTokens(messages) > AutoCompactThreshold {
    messages = CompactMessages(messages, keepRecent)
}

// 3. 子Agent的MaxTurns限制
if isSubAgent {
    maxTurns = 32  // 硬限制
}
```

## 其他需补齐的差异点

### 权限系统增强

```go
// 从3级扩展到5级，增加模式规则引擎
type PermissionMode string
const (
    ModeReadOnly         PermissionMode = "read_only"          // 新增
    ModeWorkspaceWrite   PermissionMode = "workspace_write"    // 新增
    ModeDefault          PermissionMode = "default"
    ModePlan             PermissionMode = "plan"
    ModeFullAuto         PermissionMode = "full_auto"
)

// 新增：模式规则语法 "bash(git:*)" 表示允许bash执行git开头的命令
type PermissionRule struct {
    Tool    string   `json:"tool"`
    Pattern string   `json:"pattern,omitempty"`  // fnmatch pattern on arguments
    Action  string   `json:"action"`             // "allow" | "deny" | "ask"
}
```

### Team/Cron 注册表

```go
// pkg/tasks/team_registry.go
type TeamEntry struct {
    ID       string   `json:"id"`
    Name     string   `json:"name"`
    TaskIDs  []string `json:"task_ids"`
    Deleted  bool     `json:"deleted"`
}

// pkg/tasks/cron_registry.go
type CronEntry struct {
    ID       string    `json:"id"`
    Schedule string    `json:"schedule"`  // cron表达式
    Prompt   string    `json:"prompt"`
    Enabled  bool      `json:"enabled"`
    LastRun  time.Time `json:"last_run"`
    RunCount int       `json:"run_count"`
}
```

### 自动压缩增强

```go
// 在 engine.RunQuery 中添加自动压缩逻辑
const AutoCompactThreshold = 100_000 // tokens

func (e *QueryEngine) maybeAutoCompact(messages []*ConversationMessage) []*ConversationMessage {
    tokens := EstimateMessageTokens(messages)
    if tokens > AutoCompactThreshold {
        return CompactMessages(messages, 10) // 保留最近10条
    }
    return messages
}
```
