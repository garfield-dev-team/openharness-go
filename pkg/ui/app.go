package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/openharness/openharness/pkg/config"
	"github.com/openharness/openharness/pkg/engine"
	"github.com/openharness/openharness/pkg/services"
)

// RunPrintMode runs in non-interactive mode: sends the prompt, prints the
// response, and exits. Mirrors Python ui/app.py print mode.
func RunPrintMode(ctx context.Context, settings *config.Settings, prompt string, outputFormat string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	rt, err := BuildRuntime(settings, cwd)
	if err != nil {
		return err
	}
	defer rt.Close()

	if err := rt.Start(ctx); err != nil {
		return err
	}

	ch := rt.Engine.SubmitMessage(ctx, prompt)

	switch outputFormat {
	case "json":
		return printJSON(ch)
	case "stream-json":
		return printStreamJSON(ch)
	default:
		err := printText(ch)
		if err == nil {
			currentTokens := rt.Engine.CurrentTokens()
			threshold := services.DefaultCompactionConfig().TokenThreshold
			pct := float64(currentTokens) / float64(threshold) * 100
			
			color := "\033[32m" // green
			if pct > 80 {
				color = "\033[31m" // red
			} else if pct > 50 {
				color = "\033[33m" // yellow
			}
			
			fmt.Printf("\n\033[90m[🧠 Brain Capacity] %s%.1f%%\033[90m (%d / %d tokens)\033[0m\n", color, pct, currentTokens, threshold)
		}
		return err
	}
}

func printText(ch <-chan engine.StreamEventWithUsage) error {
	for ev := range ch {
		if ev.Event.Error != nil {
			return ev.Event.Error
		}
		switch ev.Event.Type {
		case engine.EventTextDelta:
			fmt.Print(ev.Event.Text)
		case engine.EventToolExecutionStarted:
			argsStr := string(ev.Event.ToolInput)
			if len(argsStr) > 200 {
				argsStr = argsStr[:200] + "..."
			}
			// ANSI colors: 90 is dark gray (dim), 33 is yellow
			fmt.Printf("\n\033[90m▶ \033[33m%s\033[90m(%s)\033[0m\n", ev.Event.ToolName, argsStr)
		case engine.EventToolExecutionCompleted:
			if ev.Event.ToolResult != nil && ev.Event.ToolResult.IsError {
				// 31 is red
				fmt.Printf("\033[90m✖ \033[31m%s\033[90m failed\033[0m\n", ev.Event.ToolName)
			} else {
				// 32 is green
				fmt.Printf("\033[90m✔ \033[32m%s\033[90m completed\033[0m\n", ev.Event.ToolName)
			}
		}
	}
	fmt.Println()
	return nil
}

func printJSON(ch <-chan engine.StreamEventWithUsage) error {
	var fullText strings.Builder
	for ev := range ch {
		if ev.Event.Error != nil {
			return ev.Event.Error
		}
		if ev.Event.Type == engine.EventTextDelta {
			fullText.WriteString(ev.Event.Text)
		}
	}
	out := map[string]any{"role": "assistant", "content": fullText.String()}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func printStreamJSON(ch <-chan engine.StreamEventWithUsage) error {
	enc := json.NewEncoder(os.Stdout)
	for ev := range ch {
		if ev.Event.Error != nil {
			return ev.Event.Error
		}
		_ = enc.Encode(map[string]any{
			"type": string(ev.Event.Type),
			"text": ev.Event.Text,
		})
	}
	return nil
}

// RunREPL starts an interactive read-eval-print loop.
func RunREPL(ctx context.Context, settings *config.Settings) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	rt, err := BuildRuntime(settings, cwd)
	if err != nil {
		return err
	}
	defer rt.Close()

	if err := rt.Start(ctx); err != nil {
		return err
	}

	fmt.Printf("openharness v0.1.0 | model: %s | cwd: %s\n", settings.Model, cwd)
	fmt.Println("Type /help for commands, Ctrl-D to exit.")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/exit" || line == "/quit" {
			break
		}
		if line == "/clear" {
			rt.Engine.Clear()
			fmt.Println("Conversation cleared.")
			continue
		}
		if line == "/help" {
			printHelp()
			continue
		}
		if line == "/cost" {
			currentTokens := rt.Engine.CurrentTokens()
			threshold := services.DefaultCompactionConfig().TokenThreshold
			fmt.Printf("Current memory tokens: %d / %d\n", currentTokens, threshold)
			continue
		}

		if err := rt.HandleLine(ctx, line); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner: %w", err)
	}
	return nil
}

func printHelp() {
	fmt.Println("Commands:")
	fmt.Println("  /clear   Clear conversation history")
	fmt.Println("  /cost    Show token usage")
	fmt.Println("  /help    Show this help")
	fmt.Println("  /exit    Exit the REPL")
}
