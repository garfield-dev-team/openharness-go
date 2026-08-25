package onboard_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openharness/openharness/pkg/config"
	"github.com/openharness/openharness/pkg/onboard"
)

type fakeIO struct {
	in   *bufio.Reader
	out  bytes.Buffer
	saved *config.Settings
	saveErr error
}

func runWizard(t *testing.T, input string, validate func(context.Context, *config.Settings) error) (*fakeIO, error) {
	t.Helper()
	f := &fakeIO{in: bufio.NewReader(strings.NewReader(input))}
	opts := onboard.Options{
		In:       f.in,
		Out:      &f.out,
		Terminal: true,
		Save: func(s *config.Settings) error {
			f.saved = s
			return f.saveErr
		},
		Validate: validate,
	}
	settings := config.DefaultSettings()
	err := onboard.Run(&settings, opts)
	return f, err
}

func TestWizardHappyPathOpenAICompatible(t *testing.T) {
	var validated *config.Settings
	f, err := runWizard(t, strings.Join([]string{
		"7",                         // provider: custom openai-compatible
		"https://api.deepseek.com",  // base URL
		"sk-test-123",               // api key
		"deepseek-chat",             // model
		"",                          // (validation succeeds; no retry prompt)
	}, "\n"), func(_ context.Context, s *config.Settings) error {
		validated = s
		return nil
	})
	if err != nil {
		t.Fatalf("wizard failed: %v", err)
	}

	if f.saved == nil {
		t.Fatal("settings were not saved")
	}
	if f.saved.Provider != "openai-compatible" || f.saved.BaseURL == nil || *f.saved.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("provider/base URL not captured: %+v", f.saved)
	}
	if f.saved.APIKey != "sk-test-123" || f.saved.Model != "deepseek-chat" {
		t.Fatalf("key/model not captured: %+v", f.saved)
	}
	if validated == nil || validated.APIKey != "sk-test-123" {
		t.Fatal("validate was not called with the entered credentials")
	}
	if !strings.Contains(f.out.String(), "Saved.") {
		t.Fatalf("missing save confirmation: %q", f.out.String())
	}
}

// Picking a built-in preset must pre-fill the base URL and default model:
// the only required input is the API key.
func TestWizardPresetOnlyNeedsAPIKey(t *testing.T) {
	f, err := runWizard(t, strings.Join([]string{
		"4",        // OpenRouter
		"sk-or-v1-x", // api key — the only required input
		"",         // keep preset default model
		"",
	}, "\n"), func(_ context.Context, _ *config.Settings) error { return nil })
	if err != nil {
		t.Fatalf("wizard failed: %v", err)
	}

	s := f.saved
	if s.Provider != "openai-compatible" {
		t.Fatalf("preset provider not applied: %+v", s)
	}
	if s.BaseURL == nil || *s.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("preset base URL not applied: %+v", s.BaseURL)
	}
	if s.APIKey != "sk-or-v1-x" {
		t.Fatalf("key not captured: %q", s.APIKey)
	}
	if s.Model != "anthropic/claude-sonnet-4" {
		t.Fatalf("preset default model not applied: %q", s.Model)
	}
	if !strings.Contains(f.out.String(), "Provider: openrouter") {
		t.Fatalf("summary missing preset name: %q", f.out.String())
	}
}

func TestWizardAnthropicKeepsDefaults(t *testing.T) {
	f, err := runWizard(t, "2\nsk-ant\n\n", func(_ context.Context, _ *config.Settings) error { return nil })
	if err != nil {
		t.Fatalf("wizard failed: %v", err)
	}
	if f.saved.Provider != "" || f.saved.BaseURL != nil {
		t.Fatalf("anthropic path must not set provider/base URL: %+v", f.saved)
	}
	// Empty model input keeps the default.
	if f.saved.Model != config.DefaultSettings().Model {
		t.Fatalf("model default not applied: %q", f.saved.Model)
	}
}

func TestWizardRetriesOnValidationFailureThenSucceeds(t *testing.T) {
	calls := 0
	input := strings.Join([]string{
		"2",
		"bad-key",     // attempt 1 fails
		"",            // keep default model
		"y",           // retry
		"good-key",    // attempt 2 passes
		"",
	}, "\n")
	f, err := runWizard(t, input, func(_ context.Context, s *config.Settings) error {
		calls++
		if s.APIKey == "bad-key" {
			return errors.New("401 unauthorized")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("wizard failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 validation calls, got %d", calls)
	}
	if f.saved.APIKey != "good-key" {
		t.Fatalf("retry did not persist the corrected key: %+v", f.saved)
	}
	if !strings.Contains(f.out.String(), "Validation failed") {
		t.Fatalf("failure not surfaced to the user: %q", f.out.String())
	}
}

func TestWizardGivesUpAfterThreeFailedAttempts(t *testing.T) {
	calls := 0
	input := strings.Join([]string{"2", "k1", "", "y", "k2", "", "y", "k3", ""}, "\n")
	_, err := runWizard(t, input, func(_ context.Context, _ *config.Settings) error {
		calls++
		return errors.New("401 unauthorized")
	})
	if err == nil {
		t.Fatal("wizard should fail after repeated validation failures")
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
	if !strings.Contains(err.Error(), "rejected after 3 attempt") {
		t.Fatalf("error should explain the give-up: %v", err)
	}
}

func TestNeededDetectsMissingKey(t *testing.T) {
	withKey := config.DefaultSettings()
	withKey.APIKey = "x"
	if onboard.Needed(&withKey) {
		t.Fatal("configured key should not need onboarding")
	}
	withoutKey := config.DefaultSettings()
	if !onboard.Needed(&withoutKey) {
		t.Fatal("missing key should trigger onboarding")
	}
}

func TestWizardOpenCodeNeedsNoKey(t *testing.T) {
	f, err := runWizard(t, strings.Join([]string{
		"1", // OpenCode Zen (free)
		"",  // keep preset default model muse-spark-1.2-contributor-free
	}, "\n"), func(_ context.Context, s *config.Settings) error {
		if s.APIKey != "" {
			t.Fatalf("opencode preset should not require a key, got %q", s.APIKey)
		}
		if s.Provider != "opencode" {
			t.Fatalf("provider not applied: %+v", s)
		}
		if s.BaseURL == nil || *s.BaseURL != "https://opencode.ai/zen/v1" {
			t.Fatalf("opencode base URL not applied: %+v", s.BaseURL)
		}
		if s.Model != "muse-spark-1.2-contributor-free" {
			t.Fatalf("model not applied: %q", s.Model)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("wizard failed: %v", err)
	}
	if f.saved.Provider != "opencode" || f.saved.APIKey != "" {
		t.Fatalf("saved settings incorrect: %+v", f.saved)
	}
	if !strings.Contains(f.out.String(), "Provider: opencode") {
		t.Fatalf("summary missing opencode preset name: %q", f.out.String())
	}
}

func TestNeededSkipsForOpencode(t *testing.T) {
	opencode := config.DefaultSettings()
	opencode.Provider = "opencode"
	opencode.BaseURL = func() *string { s := "https://opencode.ai/zen/v1"; return &s }()
	opencode.Model = "muse-spark-1.2-contributor-free"
	opencode.APIKey = ""
	if onboard.Needed(&opencode) {
		t.Fatal("opencode provider should not need onboarding without a key")
	}
}
