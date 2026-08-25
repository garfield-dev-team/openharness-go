package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/openharness/openharness/pkg/config"
	"github.com/openharness/openharness/pkg/engine"
	"github.com/openharness/openharness/pkg/hitl"
	"github.com/openharness/openharness/pkg/logger"
	"github.com/openharness/openharness/pkg/protocol"
	"github.com/openharness/openharness/pkg/services"
	"github.com/openharness/openharness/pkg/tools"
)

// RunPrintMode runs in non-interactive mode: sends the prompt, prints the
// response, and exits.
func RunPrintMode(ctx context.Context, settings *config.Settings, prompt string, outputFormat string, rtOpts ...RuntimeOption) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	rt, err := BuildRuntime(settings, cwd, rtOpts...)
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
		out := rt.Logger
		if out == nil {
			out = logger.New(os.Stdout, logger.LevelInfo)
		}
		err := printText(ch, out)
		if err == nil {
			currentTokens := rt.Engine.CurrentTokens()
			threshold := rt.Engine.CompactionThreshold()
			pct := float64(currentTokens) / float64(threshold) * 100

			color := "\033[32m" // green
			if pct > 80 {
				color = "\033[31m" // red
			} else if pct > 50 {
				color = "\033[33m" // yellow
			}

			out.Printf("\n\033[90m[🧠 Brain Capacity] %s%.1f%%\033[90m (%d / %d tokens)\033[0m\n", color, pct, currentTokens, threshold)
		}
		return err
	}
}

func printText(ch <-chan engine.StreamEventWithUsage, out logger.Logger) error {
	if out == nil {
		out = logger.New(os.Stdout, logger.LevelInfo)
	}
	for ev := range ch {
		if ev.Event.Error != nil {
			return ev.Event.Error
		}
		switch ev.Event.Type {
		case engine.EventTextDelta:
			out.Print(ev.Event.Text)
		case engine.EventToolExecutionStarted:
			argsStr := string(ev.Event.ToolInput)
			if len(argsStr) > 200 {
				argsStr = argsStr[:200] + "..."
			}
			// ANSI colors: 90 is dark gray (dim), 33 is yellow
			out.Printf("\n\033[90m▶ \033[33m%s\033[90m(%s)\033[0m\n", ev.Event.ToolName, argsStr)
		case engine.EventToolExecutionCompleted:
			if ev.Event.ToolResult != nil && ev.Event.ToolResult.IsError {
				// 31 is red
				out.Printf("\033[90m✖ \033[31m%s\033[90m failed\033[0m\n", ev.Event.ToolName)
			} else {
				// 32 is green
				out.Printf("\033[90m✔ \033[32m%s\033[90m completed\033[0m\n", ev.Event.ToolName)
			}
		}
	}
	out.Println()
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
//
// Signal handling is two-stage: the first Ctrl-C aborts the running query
// (the session survives); a Ctrl-C while idle exits the REPL.
func RunREPL(ctx context.Context, settings *config.Settings, rtOpts ...RuntimeOption) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	cliAdapter := hitl.NewCLIAdapter(os.Stdin, os.Stdout)
	rt, err := BuildRuntime(settings, cwd, append([]RuntimeOption{WithHITLCallbacks(cliAdapter.AskUser, cliAdapter.AskPermission)}, rtOpts...)...)
	if err != nil {
		return err
	}
	defer rt.Close()

	if err := rt.Start(ctx); err != nil {
		return err
	}

	sessionCtx, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	var queryRunning atomic.Bool
	go func() {
		for range sigCh {
			if queryRunning.Load() {
				rt.Engine.Cancel()
			} else {
				cancelSession()
				return
			}
		}
	}()

	outLog := rt.Logger
	if outLog == nil {
		outLog = logger.New(os.Stdout, logger.LevelInfo)
	}
	diagLog := logger.Default()
	outLog.Printf("openharness v0.1.0 | model: %s | cwd: %s\n", settings.Model, cwd)
	outLog.Println("Type /help for commands, Ctrl-D to exit.")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		outLog.Print("> ")
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
			outLog.Println("Conversation cleared.")
			continue
		}
		if line == "/help" {
			printHelp(outLog)
			continue
		}
		if line == "/cost" {
			currentTokens := rt.Engine.CurrentTokens()
			threshold := rt.Engine.CompactionThreshold()
			outLog.Printf("Current memory tokens: %d / %d\n", currentTokens, threshold)
			continue
		}
		if line == "/model" || strings.HasPrefix(line, "/model ") || line == "/models" {
			// /models is alias for listing without switching.
			isListOnly := line == "/models"
			arg := ""
			if strings.HasPrefix(line, "/model ") {
				arg = strings.TrimSpace(strings.TrimPrefix(line, "/model"))
			}
			models := services.ListKnownModels()
			if isListOnly || arg == "" {
				outLog.Println("Available models (select number to switch, or /model <id>):")
				for i, m := range models {
					cur := ""
					if m.ID == rt.Settings.Model {
						cur = "  ← current"
					}
					outLog.Printf(" %2d) %-30s %7d tokens%s\n", i+1, m.ID, m.Window, cur)
				}
				if isListOnly {
					continue
				}
				outLog.Print("Select number or model id (empty to cancel): ")
				if !scanner.Scan() {
					break
				}
				choice := strings.TrimSpace(scanner.Text())
				if choice == "" {
					outLog.Println("Cancelled.")
					continue
				}
				if n, err := strconv.Atoi(choice); err == nil && n >= 1 && n <= len(models) {
					arg = models[n-1].ID
				} else {
					arg = choice
				}
			}
			if arg == "" {
				continue
			}
			if !services.IsKnownModel(arg) {
				outLog.Printf("Warning: %q not in catalogue, switching anyway.\n", arg)
			}
			if err := rt.SwitchModel(arg, false); err != nil {
				outLog.Printf("Switch failed: %v\n", err)
				diagLog.Error("switch model", "error", err)
			} else {
				outLog.Printf("Switched to %s (%d tokens, threshold %d)\n", arg, services.ContextWindowForModel(arg), rt.Engine.CompactionThreshold())
			}
			continue
		}

		queryRunning.Store(true)
		err := rt.HandleLine(sessionCtx, line)
		queryRunning.Store(false)
		if err != nil && sessionCtx.Err() == nil {
			diagLog.Error("handle line", "error", err)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		if sessionCtx.Err() != nil {
			outLog.Println("Exiting.")
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner: %w", err)
	}
	return nil
}

func printHelp(out logger.Logger) {
	if out == nil {
		out = logger.New(os.Stdout, logger.LevelInfo)
	}
	out.Println("Commands:")
	out.Println("  /clear          Clear conversation history")
	out.Println("  /cost           Show token usage")
	out.Println("  /model [id]     Switch model (picker if no id)")
	out.Println("  /models         List available models")
	out.Println("  /help           Show this help")
	out.Println("  /exit           Exit the REPL")
}

// RunJSONLinesMode runs a full JSON-Lines protocol session for TUI/IDE/remote.
func RunJSONLinesMode(ctx context.Context, settings *config.Settings, rtOpts ...RuntimeOption) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	jlAdapter := hitl.NewJSONLinesAdapter(os.Stdin, os.Stdout)
	manager := hitl.NewManager(jlAdapter.EmitFn())
	jlAdapter.SetManager(manager)

	rt, err := BuildRuntime(settings, cwd, append([]RuntimeOption{WithHITLCallbacks(manager.AskQuestion, manager.AskPermission)}, rtOpts...)...)
	if err != nil {
		return err
	}
	defer rt.Close()

	if err := rt.Start(ctx); err != nil {
		return err
	}

	emit := jlAdapter.EmitFn()
	emit(&protocol.BackendEvent{
		Type: protocol.BEReady,
		Text: fmt.Sprintf("openharness v0.1.0 | model: %s | cwd: %s", settings.Model, cwd),
	})

	// dispatchSubmission submits a line under the given delivery kind and
	// streams its lifecycle + transcript events back over the protocol.
	dispatchSubmission := func(kind engine.SubmissionKind, line string) {
		ch := rt.Engine.Submit(kind, line)
		go func() {
			for ev := range ch {
				switch ev.Event.Type {
				case engine.EventQueued:
					emit(&protocol.BackendEvent{Type: protocol.BEQueued})
				case engine.EventDelivered:
					emit(&protocol.BackendEvent{Type: protocol.BEDelivered})
				case engine.EventUndelivered:
					emit(&protocol.BackendEvent{Type: protocol.BEError, Text: "submission aborted before delivery"})
				case engine.EventAborted:
					emit(&protocol.BackendEvent{Type: protocol.BEError, Text: "aborted"})
				case engine.EventTextDelta:
					emit(&protocol.BackendEvent{Type: protocol.BEAssistantDelta, Text: ev.Event.Text})
				case engine.EventReasoningDelta:
					emit(&protocol.BackendEvent{Type: protocol.BEReasoningDelta, Text: ev.Event.Text})
				case engine.EventToolExecutionStarted:
					emit(&protocol.BackendEvent{
						Type:     protocol.BEToolStarted,
						Text:     ev.Event.ToolName,
						Extra:    map[string]any{"tool_use_id": ev.Event.ToolUseID},
					})
				case engine.EventToolExecutionCompleted:
					emit(&protocol.BackendEvent{
						Type:     protocol.BEToolCompleted,
						Text:     ev.Event.ToolName,
						Error:    toolErrorString(ev.Event.ToolResult),
						Extra:    map[string]any{"tool_use_id": ev.Event.ToolUseID},
					})
				case engine.EventError:
					emit(&protocol.BackendEvent{Type: protocol.BEError, Error: ev.Event.Error.Error()})
				}
			}
			emit(&protocol.BackendEvent{Type: protocol.BELineComplete})
		}()
	}

	go func() {
		if err := jlAdapter.StartReadLoop(ctx); err != nil {
			// handle read loop exit
		}
	}()

	for {
		select {
		case req := <-jlAdapter.IncomingRequests():
			switch req.Type {
			case protocol.FRSubmitLine:
				dispatchSubmission(engine.SubmissionNewTurn, req.Line)
			case protocol.FRQueueMessage:
				kind := engine.SubmissionSteering
				if req.Kind == string(engine.SubmissionFollowUp) {
					kind = engine.SubmissionFollowUp
				}
				dispatchSubmission(kind, req.Line)
			case protocol.FRListModels:
				models := services.ListKnownModels()
				emit(&protocol.BackendEvent{
					Type:  protocol.BEModelsList,
					Extra: map[string]any{"models": models, "current": rt.Settings.Model},
				})
			case protocol.FRSetModel:
				model := req.Model
				if model == "" {
					model = req.Line
				}
				persist := req.Persist != nil && *req.Persist
				if model == "" {
					emit(&protocol.BackendEvent{Type: protocol.BEError, Error: "set_model: missing model"})
					continue
				}
				if !services.IsKnownModel(model) {
					// Still allow, but frontend picker would have filtered; warn via error extra?
				}
				if err := rt.SwitchModel(model, persist); err != nil {
					emit(&protocol.BackendEvent{Type: protocol.BEError, Error: err.Error()})
				} else {
					emit(&protocol.BackendEvent{Type: protocol.BESetModel, Text: model, Extra: map[string]any{"persisted": persist}})
					st := rt.AppState.Get()
					emit(&protocol.BackendEvent{
						Type:  protocol.BEStateSnapshot,
						Extra: map[string]any{"model": st.Model, "provider": st.Provider, "base_url": st.BaseURL, "auth_status": st.AuthStatus},
					})
				}
			case protocol.FRShutdown:
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func toolErrorString(r *tools.ToolResult) string {
	if r != nil && r.IsError {
		return r.Output
	}
	return ""
}
