package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Streaming messages interface (abstraction for LLM API client)
// ---------------------------------------------------------------------------

// Message represents a single chat message sent to or received from an LLM.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// SupportsStreamingMessages is the minimal interface an API client must satisfy
// so that prompt / agent hooks can call an LLM.
type SupportsStreamingMessages interface {
	// CreateMessage sends messages to the model and returns the assistant reply.
	CreateMessage(ctx context.Context, model string, messages []Message) (string, error)
}

// ---------------------------------------------------------------------------
// HookExecutionContext
// ---------------------------------------------------------------------------

// HookExecutionContext carries shared state needed by the executor.
type HookExecutionContext struct {
	// Cwd is the working directory used when executing command hooks.
	Cwd string
	// APIClient is used by prompt and agent hooks to call an LLM.
	APIClient SupportsStreamingMessages
	// DefaultModel is used when a prompt/agent hook does not specify a model.
	DefaultModel string
}

// ---------------------------------------------------------------------------
// HookExecutor
// ---------------------------------------------------------------------------

// HookExecutor runs hooks that are registered in a HookRegistry.
type HookExecutor struct {
	registry *HookRegistry
	ctx      *HookExecutionContext
}

// NewHookExecutor creates a new executor bound to the given registry and context.
func NewHookExecutor(registry *HookRegistry, execCtx *HookExecutionContext) *HookExecutor {
	return &HookExecutor{
		registry: registry,
		ctx:      execCtx,
	}
}

// Execute runs all hooks registered for the given event whose matcher matches
// the payload. It returns an AggregatedHookResult that summarises every
// individual hook run.
func (e *HookExecutor) Execute(ctx context.Context, event HookEvent, payload map[string]any) (*AggregatedHookResult, error) {
	hooks := e.registry.Get(event)
	agg := &AggregatedHookResult{}
	for _, hook := range hooks {
		if !matchesHook(hook, payload) {
			continue
		}
		var result HookResult
		var err error
		switch h := hook.(type) {
		case *CommandHookDefinition:
			result, err = e.runCommandHook(ctx, h, event, payload)
		case *HttpHookDefinition:
			result, err = e.runHTTPHook(ctx, h, event, payload)
		case *PromptHookDefinition:
			result, err = e.runPromptLikeHook(ctx, h.Prompt, h.Model, h.TimeoutSeconds, h.BlockOnFailure, event, payload, false)
			result.HookType = "prompt"
		case *AgentHookDefinition:
			result, err = e.runPromptLikeHook(ctx, h.Prompt, h.Model, h.TimeoutSeconds, h.BlockOnFailure, event, payload, true)
			result.HookType = "agent"
		default:
			return nil, fmt.Errorf("hooks: unsupported hook type %T", hook)
		}
		if err != nil {
			result = HookResult{
				HookType: hook.HookType(),
				Success:  false,
				Output:   err.Error(),
				Blocked:  hook.GetBlockOnFailure(),
				Reason:   fmt.Sprintf("hook execution failed: %v", err),
			}
		}
		agg.Results = append(agg.Results, result)
	}
	return agg, nil
}

// ---------------------------------------------------------------------------
// Command hook
// ---------------------------------------------------------------------------

func (e *HookExecutor) runCommandHook(ctx context.Context, hook *CommandHookDefinition, event HookEvent, payload map[string]any) (HookResult, error) {
	timeout := time.Duration(hook.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payloadJSON, _ := json.Marshal(payload)

	cmd := exec.CommandContext(ctx, "bash", "-lc", hook.Command)
	cmd.Dir = e.ctx.Cwd
	cmd.Env = append(os.Environ(),
		"OPENHARNESS_HOOK_EVENT="+string(event),
		"OPENHARNESS_HOOK_PAYLOAD="+string(payloadJSON),
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n" + stderr.String()
	}
	output = strings.TrimSpace(output)

	if err != nil {
		return HookResult{
			HookType: "command",
			Success:  false,
			Output:   output,
			Blocked:  hook.BlockOnFailure,
			Reason:   fmt.Sprintf("command exited with error: %v", err),
		}, nil
	}

	return HookResult{
		HookType: "command",
		Success:  true,
		Output:   output,
	}, nil
}

// ---------------------------------------------------------------------------
// HTTP hook
// ---------------------------------------------------------------------------

func (e *HookExecutor) runHTTPHook(ctx context.Context, hook *HttpHookDefinition, event HookEvent, payload map[string]any) (HookResult, error) {
	timeout := time.Duration(hook.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, _ := json.Marshal(map[string]any{
		"event":   string(event),
		"payload": payload,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, bytes.NewReader(body))
	if err != nil {
		return HookResult{}, fmt.Errorf("http hook: failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hook.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return HookResult{
			HookType: "http",
			Success:  false,
			Output:   err.Error(),
			Blocked:  hook.BlockOnFailure,
			Reason:   fmt.Sprintf("http hook request failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	output := strings.TrimSpace(string(respBody))

	if resp.StatusCode >= 400 {
		return HookResult{
			HookType: "http",
			Success:  false,
			Output:   output,
			Blocked:  hook.BlockOnFailure,
			Reason:   fmt.Sprintf("http hook returned status %d", resp.StatusCode),
		}, nil
	}

	return HookResult{
		HookType: "http",
		Success:  true,
		Output:   output,
	}, nil
}

// ---------------------------------------------------------------------------
// Prompt / Agent hook (LLM-based)
// ---------------------------------------------------------------------------

func (e *HookExecutor) runPromptLikeHook(
	ctx context.Context,
	prompt string,
	model *string,
	timeoutSeconds int,
	blockOnFailure bool,
	event HookEvent,
	payload map[string]any,
	agentMode bool,
) (HookResult, error) {
	if e.ctx.APIClient == nil {
		return HookResult{}, fmt.Errorf("prompt/agent hook requires an API client")
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resolvedModel := e.ctx.DefaultModel
	if model != nil && *model != "" {
		resolvedModel = *model
	}

	// Inject arguments into the prompt template.
	resolvedPrompt := injectArguments(prompt, payload)

	systemMsg := "You are a hook evaluator. Evaluate the following condition and respond with a JSON object: {\"ok\": true} if the condition passes, or {\"ok\": false, \"reason\": \"...\"} if it does not."
	if agentMode {
		systemMsg = "You are a hook agent. Evaluate the following condition using any tools available. Respond with a JSON object: {\"ok\": true} if the condition passes, or {\"ok\": false, \"reason\": \"...\"} if it does not."
	}

	messages := []Message{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: resolvedPrompt},
	}

	response, err := e.ctx.APIClient.CreateMessage(ctx, resolvedModel, messages)
	if err != nil {
		return HookResult{}, fmt.Errorf("LLM call failed: %w", err)
	}

	parsed := parseHookJSON(response)
	ok, _ := parsed["ok"].(bool)
	reason, _ := parsed["reason"].(string)

	if !ok {
		return HookResult{
			Success: false,
			Output:  response,
			Blocked: blockOnFailure,
			Reason:  reason,
		}, nil
	}
	return HookResult{
		Success: true,
		Output:  response,
	}, nil
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// matchesHook checks if the hook's matcher pattern matches the payload.
// The matcher is compared against the "tool_name" key in the payload using
// filepath.Match (similar to Python's fnmatch.fnmatch).
func matchesHook(hook HookDefinition, payload map[string]any) bool {
	matcher := hook.GetMatcher()
	if matcher == nil {
		return true // no matcher means always match
	}
	subject, _ := payload["tool_name"].(string)
	if subject == "" {
		return false
	}
	matched, err := filepath.Match(*matcher, subject)
	if err != nil {
		return false
	}
	return matched
}

// injectArguments replaces $ARGUMENTS in the template with the JSON
// representation of the payload.
func injectArguments(template string, payload map[string]any) string {
	payloadJSON, _ := json.Marshal(payload)
	return strings.ReplaceAll(template, "$ARGUMENTS", string(payloadJSON))
}

// jsonBlockRe matches a JSON code block or a bare JSON object in text.
var jsonBlockRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```|^(\\{.*\\})$")

// parseHookJSON attempts to extract a JSON object from the LLM response text.
// It first tries to find a JSON code block, then a bare JSON object. If that
// fails it returns a simple interpretation based on whether the text looks
// affirmative.
func parseHookJSON(text string) map[string]any {
	text = strings.TrimSpace(text)

	// Try code-block extraction first.
	if m := jsonBlockRe.FindStringSubmatch(text); m != nil {
		candidate := m[1]
		if candidate == "" {
			candidate = m[2]
		}
		var result map[string]any
		if json.Unmarshal([]byte(candidate), &result) == nil {
			return result
		}
	}

	// Try direct JSON parse.
	var result map[string]any
	if json.Unmarshal([]byte(text), &result) == nil {
		return result
	}

	// Fallback: interpret plain text.
	lower := strings.ToLower(text)
	if strings.Contains(lower, "ok") || strings.Contains(lower, "pass") || strings.Contains(lower, "true") {
		return map[string]any{"ok": true}
	}
	return map[string]any{"ok": false, "reason": text}
}
