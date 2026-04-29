// Package subagent 实现 subagent 工具。
//
// 设计目标：给主 agent 一个 "dispatch_agent" 工具，用来在隔离的子会话中
// 完成一个明确的子任务，返回最终结论。子 agent 拥有独立的消息历史与 ReAct 循环，
// 避免长时程主会话被中间细节淹没。
package subagent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// Config subagent 构建所需的依赖。
type Config struct {
	ChatModel model.ToolCallingChatModel
	// ChildTools 是允许 subagent 使用的工具白名单；通常是主 agent 的
	// 文件系统只读工具，不含危险写操作。
	ChildTools []tool.BaseTool
	MaxStep    int
	System     string
}

type dispatchTool struct {
	cfg Config
}

// NewDispatchTool 创建一个 dispatch_agent 工具，调用时会启动一个
// 全新的 ReAct subagent 去完成子任务，再返回总结。
func NewDispatchTool(cfg Config) tool.InvokableTool {
	if cfg.MaxStep <= 0 {
		cfg.MaxStep = 20
	}
	if cfg.System == "" {
		cfg.System = `你是一个 subagent。专注完成调用者给出的子任务，
自主决定调用多少次工具。完成后直接以自然语言返回结论，不要再追问。`
	}
	return &dispatchTool{cfg: cfg}
}

func (t *dispatchTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "dispatch_agent",
		Desc: "派发一个隔离的子 agent 去完成一个明确的子任务（例如：在代码库中定位某个函数）。返回子 agent 的最终结论。适合做范围探索，避免把中间过程污染主会话。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"prompt": {Type: schema.String, Desc: "子任务描述（越明确越好）", Required: true},
		}),
	}, nil
}

func (t *dispatchTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return errJSON(err), nil
	}
	if args.Prompt == "" {
		return errJSON(fmt.Errorf("prompt must not be empty")), nil
	}

	invokables := make([]tool.BaseTool, 0, len(t.cfg.ChildTools))
	invokables = append(invokables, t.cfg.ChildTools...)

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: t.cfg.ChatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: invokables,
		},
		MaxStep: t.cfg.MaxStep,
	})
	if err != nil {
		return errJSON(err), nil
	}

	resp, err := agent.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: t.cfg.System},
		{Role: schema.User, Content: args.Prompt},
	})
	if err != nil {
		return errJSON(err), nil
	}

	b, _ := json.Marshal(map[string]any{
		"ok":       true,
		"summary":  resp.Content,
		"steps":    t.cfg.MaxStep,
	})
	return string(b), nil
}

func errJSON(err error) string {
	b, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
	return string(b)
}
