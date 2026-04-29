// Command einoagent 是基于 cloudwego/eino ReAct 的 simple-claude-code 演示入口。
//
// 运行示例：
//
//	export OPENAI_API_KEY=sk-xxx
//	export OPENAI_BASE_URL=https://api.openai.com/v1
//	export EINO_MODEL=gpt-4o-mini
//	go run ./cmd/einoagent "分析一下当前项目的结构"
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"

	"github.com/openharness/openharness/einoagent/agent"
	"github.com/openharness/openharness/einoagent/compact"
)

func main() {
	ctx := context.Background()

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "OPENAI_API_KEY is required")
		os.Exit(1)
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	modelName := os.Getenv("EINO_MODEL")
	if modelName == "" {
		modelName = "gpt-4o-mini"
	}

	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   modelName,
		Timeout: 5 * time.Minute,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "init chat model:", err)
		os.Exit(1)
	}

	cwd, _ := os.Getwd()

	ag, err := agent.New(ctx, agent.Config{
		ChatModel:    chatModel,
		Cwd:          cwd,
		SkillsRoot:   ".skills",
		MaxStep:      80,
		ReflectEvery: 5,
		Compact:      compact.DefaultConfig(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "init agent:", err)
		os.Exit(1)
	}

	// 非交互模式：命令行直接给出 prompt。
	if len(os.Args) > 1 {
		prompt := strings.Join(os.Args[1:], " ")
		runOnce(ctx, ag, prompt)
		return
	}

	// REPL 模式。
	fmt.Println("einoagent REPL — type /exit to quit.")
	reader := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !reader.Scan() {
			break
		}
		line := strings.TrimSpace(reader.Text())
		if line == "" {
			continue
		}
		if line == "/exit" || line == "/quit" {
			return
		}
		if line == "/todos" {
			for _, it := range ag.Todos() {
				fmt.Printf("  [%s] %s: %s\n", it.Status, it.ID, it.Content)
			}
			continue
		}
		runOnce(ctx, ag, line)
	}
}

func runOnce(ctx context.Context, ag *agent.Agent, prompt string) {
	resp, err := ag.Run(ctx, prompt)
	if err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		return
	}
	fmt.Println()
	fmt.Println(resp.Content)
	fmt.Println()
}
