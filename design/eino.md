# Eino ReAct Agent

```go
package codebase_agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	fornax_adapter "code.byted.org/ecom/e_tars_agents/adapter/fornax"
	"code.byted.org/ecom/e_tars_agents/common/constants"
	"code.byted.org/ecom/e_tars_agents/kitex_gen/agent"
	"code.byted.org/ecom/e_tars_agents/kitex_gen/base"
	tools_git "code.byted.org/ecom/e_tars_agents/tools/git"
	tools_ls "code.byted.org/ecom/e_tars_agents/tools/ls"
	tools_read "code.byted.org/ecom/e_tars_agents/tools/read"
	"code.byted.org/flowdevops/fornax_sdk/utils/log"
	"code.byted.org/flowdevops/fornax_sdk/utils/ptr"
	"code.byted.org/gopkg/logs/v2"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

type CodebaseAgentParams struct {
	UserQuery string `json:"user_query"` // 用户查询意图
}

func CallCodebaseAgent(ctx context.Context, req *agent.AgentRequest) (*agent.AgentSyncResponse, error) {

	var params CodebaseAgentParams
	err := json.Unmarshal([]byte(req.AgentParamsString), &params)

	if err != nil {
		log.CtxError(ctx, "[CallCodebaseAgent] failed to unmarshal params: %v", err)
		return nil, err
	}

	systemPrompt, err := fornax_adapter.NewFornaxAdapter().GetFornaxSystemPrompt(ctx, constants.GeneCodebasePromptKey)
	if err != nil {
		log.CtxError(ctx, "[CallCodebaseAgent] failed to get fornax system prompt: %v", err)
		return nil, err
	}
	log.CtxInfo(ctx, "[CallCodebaseAgent] fornax system prompt: %s", systemPrompt)

	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		// BaseURL:     "https://gpt-i18n.byteintl.net/gpt/openapi/online/v2/crawl/openai/deployments/gpt_openapi",
		// APIKey:      "DVGIlFQ61RClua1RJVf0X7XWjksao0g6_GPT_AK",
		// Model:       "gcp-claude4-sonnet",
		// BaseURL: "https://ark-cn-beijing.bytedance.net/api/v3",
		// APIKey:  "0d22638d-34fe-4652-9de0-24f7d78ba9ab",
		// Model:   "ep-20251009155207-ns7rf", // kimi v2
		BaseURL: "https://search-va.byteintl.net/gpt/openapi/online/v2/crawl/openai/deployments/gpt_openapi",
		APIKey:  constants.OpenAiApiKey,
		Model:   constants.GeminiModel, // gemini 2.5 pro
		// Model:       "gpt-5-codex-2025-09-15", // gpt 5 codex
		Timeout:     30 * time.Minute,
		MaxTokens:   ptr.FromInt(8192),
		Temperature: larkcore.Float32Ptr(1),
	})
	if err != nil {
		fmt.Println(err)
		log.CtxError(ctx, "[CallCodebaseAgent] failed to new chat model: %v", err)
		return nil, err
	}

	gitTool, err := tools_git.CreateTool()

	if err != nil {
		log.CtxError(ctx, "[CallCodebaseAgent] failed to new git tool: %v", err)
		return nil, err
	}

	lsTool, err := tools_ls.CreateTool()

	if err != nil {
		log.CtxError(ctx, "[CallCodebaseAgent] failed to new ls tool: %v", err)
		return nil, err
	}

	readTool, err := tools_read.CreateTool()

	if err != nil {
		log.CtxError(ctx, "[CallCodebaseAgent] failed to new read tool: %v", err)
		return nil, err
	}

	reActAgent, err := react.NewAgent(ctx, &react.AgentConfig{
		Model:            chatModel,
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: []tool.BaseTool{
				gitTool,
				lsTool,
				readTool,
			},
		},
		MaxStep: 100,
	})
	if err != nil {
		log.CtxError(ctx, "[CallCodebaseAgent] failed to new agent: %v", err)
		return nil, err
	}

	res, err := reActAgent.Generate(ctx, []*schema.Message{
		{
			Role:    schema.System,
			Content: systemPrompt,
		},
		{
			Role:    schema.User,
			Content: params.UserQuery,
		},
	})
	if err != nil {
		log.CtxError(ctx, "[CallCodebaseAgent] failed to generate: %v", err)
		return nil, err
	}

	logs.CtxWarn(ctx, "[CallCodebaseAgent] generate result: %v", res)

	return &agent.AgentSyncResponse{
		OriginalOutput: res.String(),
		LlmResp: &base.LlmResp{
			Role:         string(res.Role),
			Content:      res.Content,
			FinishReason: res.ResponseMeta.FinishReason,
			Usage: &base.TokenUsage{
				TotalTokens:      int32(res.ResponseMeta.Usage.TotalTokens),
				PromptTokens:     int32(res.ResponseMeta.Usage.PromptTokens),
				CompletionTokens: int32(res.ResponseMeta.Usage.CompletionTokens),
			},
		},
	}, nil
}

type BranchCompareAgentParams struct {
	RepoURL  string `json:"repo_url"`        // 仓库URL
	RepoName string `json:"repo_name"`       // 仓库名称
	Branch1  string `json:"branch1"`         // 第一个分支
	Branch2  string `json:"branch2"`         // 第二个分支
	BugInfo  string `json:"bug_info"`        // 缺陷信息
	Depth    int    `json:"depth,omitempty"` // 克隆深度
}

// CallBranchCompareAgent 调用分支比较Agent，专门用于比较两个分支的差异并分析可能的缺陷
func CallBranchCompareAgent(ctx context.Context, req *agent.AgentRequest) (*agent.AgentSyncResponse, error) {
	var params BranchCompareAgentParams
	err := json.Unmarshal([]byte(req.AgentParamsString), &params)
	if err != nil {
		log.CtxError(ctx, "[CallBranchCompareAgent] failed to unmarshal params: %v", err)
		return nil, err
	}

	// 获取系统提示词
	// systemPrompt, err := fornax_adapter.NewFornaxAdapter().GetFornaxSystemPrompt(ctx, constants.CodeBranchDiff)
	// if err != nil {
	// 	log.CtxError(ctx, "[CallBranchCompareAgent] failed to get fornax system prompt: %v", err)
	// 	return nil, err
	// }
	systemPrompt := `# 角色:
你是一名代码分析师，擅长分析两个分支之间的Diff信息。

## 目标:
- 分析两个分支之间的代码差异
- 基于用户描述找到与描述相关联的代码位置和片段

## 技能:
- compare_branch_diff工具获取分支之间的差异
- 能基于描述的关键词匹配对应的代码文件位置

## 特别注意：
- 不在Diff范围内的代码不在查找范围

## 输出结果：
请提供具体的文件路径，而不是目录。返回格式请使用json格式，示例如下：
{
    "files": [
        {
            "path": "匹配的文件路径",
            "confidence": 分数,
            "reason": "分析理由"
        }
    ]
}`
	log.CtxInfo(ctx, "[CallBranchCompareAgent] fornax system prompt: %s", systemPrompt)

	// 创建LLM模型
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL:     "https://search-va.byteintl.net/gpt/openapi/online/v2/crawl/openai/deployments/gpt_openapi",
		APIKey:      constants.OpenAiApiKey,
		Model:       constants.GeminiModel, // gemini 2.5 pro
		Timeout:     30 * time.Minute,
		MaxTokens:   ptr.FromInt(8192),
		Temperature: larkcore.Float32Ptr(1),
	})
	if err != nil {
		fmt.Println(err)
		log.CtxError(ctx, "[CallBranchCompareAgent] failed to new chat model: %v", err)
		return nil, err
	}

	gitTool, err := tools_git.CreateTool()

	if err != nil {
		log.CtxError(ctx, "[CallCodebaseAgent] failed to new git tool: %v", err)
		return nil, err
	}

	// 创建必要的工具
	// branchCompareTool, err := tools_git.CreateBranchCompareTool()
	branchDiffTool, err := tools_git.CreateBranchDiffTool()
	if err != nil {
		log.CtxError(ctx, "[CallBranchCompareAgent] failed to new branch compare tool: %v", err)
		return nil, err
	}

	readTool, err := tools_read.CreateTool()
	if err != nil {
		log.CtxError(ctx, "[CallBranchCompareAgent] failed to new read tool: %v", err)
		return nil, err
	}

	// 创建专门用于分支比较的Agent
	reActAgent, err := react.NewAgent(ctx, &react.AgentConfig{
		Model:            chatModel,
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: []tool.BaseTool{
				gitTool,
				branchDiffTool,
				readTool,
			},
		},
		MaxStep: 100,
	})
	if err != nil {
		log.CtxError(ctx, "[CallBranchCompareAgent] failed to new agent: %v", err)
		return nil, err
	}

	// 构建用户查询，明确要求进行分支比较和缺陷分析
	userQuery := fmt.Sprintf("请帮我分析仓库 %s (地址: %s)中可能导致问题的代码文件。\n"+
		"缺陷信息: %s\n"+
		"请使用compare_branch_diff工具获取分支 %s 和分支 %s 的差异\n"+
		"然后分析差异文件，重点关注与缺陷描述相关的代码变更\n"+
		"找出最有可能存在问题的文件，并按照问题的可能性排序\n"+
		"为每个文件提供以下信息:\n"+
		"1. 文件路径\n"+
		"2. 文件名称\n"+
		"3. 问题可能性评分 (0-100)\n"+
		"4. 为什么这个文件可能存在问题的详细分析理由，特别是与分支差异的关联\n"+
		"请提供具体的文件路径，而不是目录。返回格式请使用json格式，例如:\n"+
		`{"files":[{"path":"匹配的文件路径","confidence":分数,"reason":"分析理由"}]}`,
		params.RepoName, params.RepoURL, params.BugInfo, params.Branch1, params.Branch2)

	// 执行Agent生成
	res, err := reActAgent.Generate(ctx, []*schema.Message{
		{
			Role:    schema.System,
			Content: systemPrompt,
		},
		{
			Role:    schema.User,
			Content: userQuery,
		},
	})
	if err != nil {
		log.CtxError(ctx, "[CallBranchCompareAgent] failed to generate: %v", err)
		return nil, err
	}

	logs.CtxWarn(ctx, "[CallBranchCompareAgent] generate result: %v", res)

	return &agent.AgentSyncResponse{
		OriginalOutput: res.String(),
		LlmResp: &base.LlmResp{
			Role:         string(res.Role),
			Content:      res.Content,
			FinishReason: res.ResponseMeta.FinishReason,
			Usage: &base.TokenUsage{
				TotalTokens:      int32(res.ResponseMeta.Usage.TotalTokens),
				PromptTokens:     int32(res.ResponseMeta.Usage.PromptTokens),
				CompletionTokens: int32(res.ResponseMeta.Usage.CompletionTokens),
			},
		},
	}, nil
}
```
