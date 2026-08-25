// Package onboard implements the interactive first-run setup wizard that
// collects provider credentials, validates them with a live request, and
// persists them to the settings file.
package onboard

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openharness/openharness/pkg/api"
	"github.com/openharness/openharness/pkg/config"
	"github.com/openharness/openharness/pkg/types"
)

// ErrNotInteractive reports that the wizard cannot run because stdin is not
// a terminal. Callers should fall back to reporting the missing-key error.
var ErrNotInteractive = errors.New("onboarding requires an interactive terminal")

// preset is a built-in provider profile: picking one only requires an API
// key — base URL and a sensible default model are pre-filled.
type preset struct {
	name     string // short identifier shown in the summary line
	label    string
	provider string // "" = Anthropic direct; otherwise settings.Provider value
	baseURL  string // "" = ask (custom) or not applicable (Anthropic direct)
	model    string
	askURL   bool // custom entry prompts for the base URL
}

// providerPresets is the menu shown by the wizard. Order is display order.
var providerPresets = []preset{
	{name: "anthropic", label: "Anthropic (direct)", model: "claude-sonnet-4-20250514"},
	{name: "openai", label: "OpenAI", provider: "openai-compatible", baseURL: "https://api.openai.com/v1", model: "gpt-4o"},
	{name: "openrouter", label: "OpenRouter", provider: "openai-compatible", baseURL: "https://openrouter.ai/api/v1", model: "anthropic/claude-sonnet-4"},
	{name: "deepseek", label: "DeepSeek", provider: "openai-compatible", baseURL: "https://api.deepseek.com/v1", model: "deepseek-chat"},
	{name: "kimi", label: "Kimi (Moonshot)", provider: "openai-compatible", baseURL: "https://api.moonshot.cn/v1", model: "kimi-k2-0711-preview"},
	{name: "custom", label: "Custom OpenAI-compatible (enter base URL)", provider: "openai-compatible", model: "gpt-4o", askURL: true},
}

// Options injects the wizard's I/O side effects for testing.
type Options struct {
	In       io.Reader                                              // defaults to os.Stdin
	Out      io.Writer                                              // defaults to os.Stdout
	Terminal bool                                                   // whether In behaves like a TTY
	Save     func(*config.Settings) error                           // defaults to config.SaveSettings
	Validate func(context.Context, *config.Settings) error          // defaults to validateCredentials
}

// Needed reports whether first-run onboarding should trigger: no usable API
// key was resolved from flags, config file, or environment.
func Needed(settings *config.Settings) bool {
	_, err := settings.ResolveAPIKey()
	return err != nil
}

// IsTerminal reports whether stdin is attached to a terminal.
func IsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Run drives the wizard and mutates settings in place. It returns
// ErrNotInteractive when input is not a terminal.
func Run(settings *config.Settings, opts Options) error {
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Save == nil {
		opts.Save = func(s *config.Settings) error { return config.SaveSettings(*s) }
	}
	if opts.Validate == nil {
		opts.Validate = validateCredentials
	}

	fmt.Fprintln(opts.Out, "Welcome to OpenHarness! Let's configure a model provider.")
	fmt.Fprintln(opts.Out)

	p, err := choosePreset(opts)
	if err != nil {
		return err
	}
	settings.Provider = p.provider
	switch {
	case p.askURL:
		baseURL := askString(opts, "Base URL", "https://api.openai.com/v1")
		settings.BaseURL = &baseURL
	case p.baseURL != "":
		baseURL := p.baseURL
		settings.BaseURL = &baseURL
	default:
		settings.BaseURL = nil
	}

	for attempt := 1; ; attempt++ {
		key := askString(opts, "API key", "")
		settings.APIKey = key

		model := askString(opts, "Model", p.model)
		settings.Model = model

		fmt.Fprintf(opts.Out, "Validating %s...\n", settings.Model)
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		err := opts.Validate(ctx, settings)
		cancel()
		if err == nil {
			break
		}
		fmt.Fprintf(opts.Out, "\nValidation failed: %v\n", err)
		if attempt >= 3 || !askYesNo(opts, "Try again?") {
			return fmt.Errorf("onboarding: credentials rejected after %d attempt(s): %w", attempt, err)
		}
	}

	if err := opts.Save(settings); err != nil {
		return fmt.Errorf("onboarding: save settings: %w", err)
	}

	fmt.Fprintln(opts.Out)
	fmt.Fprintf(opts.Out, "Saved. Provider: %s | Model: %s | Config: %s\n",
		p.name, settings.Model, config.GetConfigFilePath())
	fmt.Fprintf(opts.Out, "Run `openharness` to start.\n")
	return nil
}

const requestTimeout = 30 * time.Second

func choosePreset(opts Options) (preset, error) {
	for {
		fmt.Fprintln(opts.Out, "Select a provider:")
		for i, p := range providerPresets {
			fmt.Fprintf(opts.Out, "  %d) %s\n", i+1, p.label)
		}
		fmt.Fprint(opts.Out, "> ")

		line, err := readLine(opts)
		if err != nil {
			return preset{}, err
		}
		n := 0
		if v, convErr := strconv.Atoi(strings.TrimSpace(line)); convErr == nil {
			n = v
		}
		if n >= 1 && n <= len(providerPresets) {
			return providerPresets[n-1], nil
		}
		fmt.Fprintf(opts.Out, "Please enter a number between 1 and %d.\n", len(providerPresets))
	}
}

// askString prompts for a value; empty input keeps def when def != "",
// otherwise it re-prompts.
func askString(opts Options, label, def string) string {
	for {
		if def != "" {
			fmt.Fprintf(opts.Out, "%s [%s]: ", label, def)
		} else {
			fmt.Fprintf(opts.Out, "%s: ", label)
		}
		line, err := readLine(opts)
		if err != nil {
			return def
		}
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
		if def != "" {
			return def
		}
		fmt.Fprintln(opts.Out, "A value is required.")
	}
}

func askYesNo(opts Options, question string) bool {
	fmt.Fprintf(opts.Out, "%s [y/N]: ", question)
	line, err := readLine(opts)
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func readLine(opts Options) (string, error) {
	reader, ok := opts.In.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(opts.In)
	}
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		if errors.Is(err, io.EOF) {
			return "", ErrNotInteractive
		}
		return "", fmt.Errorf("onboarding: read input: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// validateCredentials issues a minimal streaming request through the same
// client stack the runtime will use, proving the key/model/baseURL work.
func validateCredentials(ctx context.Context, settings *config.Settings) error {
	client := buildClient(settings)
	ch, err := client.StreamMessage(ctx, &api.ApiMessageRequest{
		Model:    settings.Model,
		Messages: []types.ConversationMessage{types.FromUserText("Reply with OK.")},
		SystemPrompt: func() *string { s := "You are a connection test."; return &s }(),
		MaxTokens: 16,
	})
	if err != nil {
		return err
	}
	var got bool
	for ev := range ch {
		if ev.Err != nil {
			return fmt.Errorf("%w", ev.Err)
		}
		if ev.MessageComplete != nil {
			got = true
		}
	}
	if !got {
		return errors.New("stream ended without a completion")
	}
	return nil
}

func buildClient(settings *config.Settings) api.MessageStreamer {
	baseURL := ""
	if settings.BaseURL != nil {
		baseURL = *settings.BaseURL
	}
	key, _ := settings.ResolveAPIKey()
	if api.DetectProvider(*settings).Name == "openai-compatible" {
		return api.NewOpenAIApiClient(key, baseURL)
	}
	return api.NewAnthropicApiClient(key, baseURL)
}
