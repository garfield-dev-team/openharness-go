// Package compact 实现简易的上下文压缩。
//
// 策略（参考 Claude Code 的做法，但简化）:
//  1. 估算 token：按 4 字节 / token 的平均经验粗估。
//  2. 当 messages 的 token 估算值超过阈值 * TriggerRatio 时触发。
//  3. 保留：第一条 system、最近 KeepRecent 条消息；
//     其余用一次“摘要模型调用”压缩为单条 system/assistant 备忘录。
//  4. 为了保留前缀 KV cache，摘要消息会插入到原来位置（紧跟第一条 system 之后）。
package compact

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Config 压缩配置。
type Config struct {
	MaxTokens     int     // 硬上限
	TriggerRatio  float64 // 达到 MaxTokens * TriggerRatio 时触发
	KeepRecent    int     // 保留最近多少条原始消息
	SummaryPrompt string  // 摘要系统提示词
}

// DefaultConfig 返回开箱即用的默认配置。
func DefaultConfig() Config {
	return Config{
		MaxTokens:    32000,
		TriggerRatio: 0.8,
		KeepRecent:   8,
		SummaryPrompt: `你是一个会话摘要助手。请把下面这段对话压缩成一份"执行备忘录"，要求：
1. 保留所有用户明确提出过的需求、约束条件和关键路径；
2. 保留工具调用中发现的关键事实（文件路径、函数名、错误信息）；
3. 丢弃所有冗余的工具原始输出；
4. 输出 400-800 字的 Markdown 备忘录，不要前缀寒暄。`,
	}
}

// EstimateTokens 粗略估算一组消息的 token 数。
func EstimateTokens(msgs []*schema.Message) int {
	total := 0
	for _, m := range msgs {
		if m == nil {
			continue
		}
		total += len(m.Content) / 4
		for _, tc := range m.ToolCalls {
			total += len(tc.Function.Name)/4 + len(tc.Function.Arguments)/4
		}
	}
	return total
}

// ShouldCompact 判断是否需要压缩。
func ShouldCompact(msgs []*schema.Message, cfg Config) bool {
	if cfg.MaxTokens <= 0 {
		return false
	}
	threshold := int(float64(cfg.MaxTokens) * cfg.TriggerRatio)
	return EstimateTokens(msgs) >= threshold
}

// Compact 如果必要，则对消息列表执行摘要压缩并返回新的列表。
// summarizer 为空时，仅做截断（保留 system + 最近 KeepRecent 条）。
func Compact(ctx context.Context, msgs []*schema.Message, cfg Config, summarizer model.BaseChatModel) ([]*schema.Message, error) {
	if !ShouldCompact(msgs, cfg) {
		return msgs, nil
	}
	if len(msgs) <= cfg.KeepRecent+1 {
		return msgs, nil
	}

	var systemMsg *schema.Message
	start := 0
	if len(msgs) > 0 && msgs[0].Role == schema.System {
		systemMsg = msgs[0]
		start = 1
	}
	tailStart := len(msgs) - cfg.KeepRecent
	if tailStart < start {
		tailStart = start
	}
	middle := msgs[start:tailStart]
	tail := msgs[tailStart:]

	if len(middle) == 0 {
		return msgs, nil
	}

	summaryContent := simpleTextSummary(middle)

	if summarizer != nil {
		// 若提供了大模型摘要器，则真正调用一次生成。
		prompt := &schema.Message{
			Role:    schema.User,
			Content: fmt.Sprintf("以下是需要压缩的对话片段：\n\n%s", summaryContent),
		}
		resp, err := summarizer.Generate(ctx, []*schema.Message{
			{Role: schema.System, Content: cfg.SummaryPrompt},
			prompt,
		})
		if err == nil && resp != nil && resp.Content != "" {
			summaryContent = resp.Content
		}
	}

	summaryMsg := &schema.Message{
		Role:    schema.System,
		Content: "## 对话摘要（由 compact 模块生成，供后续推理参考）\n" + summaryContent,
	}

	result := make([]*schema.Message, 0, 2+len(tail))
	if systemMsg != nil {
		result = append(result, systemMsg)
	}
	result = append(result, summaryMsg)
	result = append(result, tail...)
	return result, nil
}

// simpleTextSummary 在缺少 LLM 摘要器时，拼接消息做简单截断。
func simpleTextSummary(msgs []*schema.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		role := string(m.Role)
		text := m.Content
		if len(text) > 400 {
			text = text[:400] + "..."
		}
		fmt.Fprintf(&b, "- [%s] %s\n", role, strings.ReplaceAll(text, "\n", " "))
	}
	return b.String()
}
