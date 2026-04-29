// Package agent 组装一个带有文件系统工具、subagent、todo 反思、
// 上下文压缩与 Skills 渐进式披露能力的 ReAct agent，面向 "simple claude code" 场景。
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/openharness/openharness/einoagent/compact"
	"github.com/openharness/openharness/einoagent/skills"
	"github.com/openharness/openharness/einoagent/subagent"
	fsTools "github.com/openharness/openharness/einoagent/tools"
	"github.com/openharness/openharness/einoagent/todo"
)

// Config 初始化 claude-code agent 所需的依赖。
type Config struct {
	ChatModel  model.ToolCallingChatModel
	Cwd        string
	SkillsRoot string // 默认 ./.skills
	MaxStep    int
	ReflectEvery int // 每 N 轮工具调用强制反思
	Compact compact.Config
}

// Agent 是一个有状态的会话体。
type Agent struct {
	cfg       Config
	skillsReg *skills.Registry
	todos     *todo.Store
	tools     []tool.BaseTool

	mu   sync.Mutex
	hist []*schema.Message
}

// New 构造 agent（含 skills 扫描、工具注册、system prompt 预构建）。
func New(ctx context.Context, cfg Config) (*Agent, error) {
	if cfg.ChatModel == nil {
		return nil, fmt.Errorf("ChatModel is required")
	}
	if cfg.Cwd == "" {
		cfg.Cwd = "."
	}
	if cfg.SkillsRoot == "" {
		cfg.SkillsRoot = ".skills"
	}
	if cfg.MaxStep <= 0 {
		cfg.MaxStep = 60
	}
	if cfg.ReflectEvery <= 0 {
		cfg.ReflectEvery = 5
	}
	if cfg.Compact.MaxTokens == 0 {
		cfg.Compact = compact.DefaultConfig()
	}

	reg, err := skills.LoadFrom(cfg.SkillsRoot)
	if err != nil {
		return nil, fmt.Errorf("load skills: %w", err)
	}

	todoStore := todo.NewStore(cfg.ReflectEvery)

	readOnly := []tool.BaseTool{
		fsTools.NewLsTool(cfg.Cwd),
		fsTools.NewReadTool(cfg.Cwd),
	}

	all := []tool.BaseTool{
		fsTools.NewLsTool(cfg.Cwd),
		fsTools.NewReadTool(cfg.Cwd),
		fsTools.NewWriteTool(cfg.Cwd),
		fsTools.NewEditTool(cfg.Cwd),
		todo.NewWriteTool(todoStore),
		skills.NewSkillTool(reg),
		subagent.NewDispatchTool(subagent.Config{
			ChatModel:  cfg.ChatModel,
			ChildTools: readOnly,
			MaxStep:    20,
		}),
	}

	return &Agent{
		cfg:       cfg,
		skillsReg: reg,
		todos:     todoStore,
		tools:     all,
	}, nil
}

// Run 对 user 输入执行一轮长时程推理。
func (a *Agent) Run(ctx context.Context, userInput string) (*schema.Message, error) {
	a.mu.Lock()
	a.hist = append(a.hist, &schema.Message{Role: schema.User, Content: userInput})
	hist := append([]*schema.Message(nil), a.hist...)
	a.mu.Unlock()

	// 在每一轮 model 调用前，执行上下文压缩 + 注入最新 system prompt + TODO 反思提醒。
	modifier := func(ctx context.Context, input []*schema.Message) []*schema.Message {
		// 1. context compaction（尽量保留前缀 KV cache：system 固定在首位）
		compacted, _ := compact.Compact(ctx, input, a.cfg.Compact, a.cfg.ChatModel)

		// 2. 构造动态 system prompt（始终保证是第一条）
		sys := a.buildSystemPrompt()
		round, needReflect := a.todos.IncRound()
		if needReflect {
			sys += fmt.Sprintf("\n\n⚠️ 强制反思：你已连续执行 %d 轮工具调用。"+
				"请立刻重新评估 todo 列表（必要时调用 `todo_write` 更新），"+
				"避免陷入死循环；如果当前任务已经完成，请直接给出最终回答。", round)
		}

		out := make([]*schema.Message, 0, len(compacted)+1)
		out = append(out, &schema.Message{Role: schema.System, Content: sys})
		for _, m := range compacted {
			if m.Role == schema.System {
				continue // 避免堆叠多条 system
			}
			out = append(out, m)
		}
		return out
	}

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: a.cfg.ChatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: a.tools,
		},
		MaxStep:         a.cfg.MaxStep,
		MessageModifier: modifier,
	})
	if err != nil {
		return nil, err
	}

	resp, err := agent.Generate(ctx, hist)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.hist = append(a.hist, resp)
	a.mu.Unlock()
	return resp, nil
}

// Todos 返回当前 TODO 快照，便于调试。
func (a *Agent) Todos() []todo.Item { return a.todos.Snapshot() }

// buildSystemPrompt 构造注入到每次模型调用的 system 消息。
func (a *Agent) buildSystemPrompt() string {
	var b strings.Builder
	b.WriteString(`你是一个 "simple-claude-code" 风格的工程助手。工作准则：
1. 先用 todo_write 规划任务（即使是简单任务也列出步骤），随后按 TODO 执行；
2. 使用 ls / read_file 先侦察再动手；使用 edit_file 做精确修改（尽量不用 write_file 覆盖）；
3. 遇到大范围探索，优先用 dispatch_agent 派发 subagent，避免中间细节污染主会话；
4. 每一轮都先思考，再调用工具；一旦任务已完成，立刻输出最终答复，不要继续调用工具；
5. 不要一次读取超过 400 行文件；不要猜测路径，先 ls；
6. 所有工具返回 JSON，请解析 ok 字段。
`)

	b.WriteString("\n### 当前工作目录\n")
	b.WriteString(a.cfg.Cwd)

	b.WriteString("\n\n### 可用 skills（调用 `skill` 工具按需加载正文）\n")
	b.WriteString(a.skillsReg.RenderCatalog())

	b.WriteString("\n\n### 当前 TODO\n")
	b.WriteString(a.todos.Render())
	return b.String()
}
