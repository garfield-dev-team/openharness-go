package services_test

import (
	"testing"

	"github.com/openharness/openharness/pkg/services"
)

func TestContextWindowForModelKnown(t *testing.T) {
	if w := services.ContextWindowForModel("claude-opus-5"); w != 1_000_000 {
		t.Fatalf("claude-opus-5 window = %d want 1000000", w)
	}
	if w := services.ContextWindowForModel("claude-fable-5"); w != 1_000_000 {
		t.Fatalf("claude-fable-5 window = %d want 1000000", w)
	}
	if w := services.ContextWindowForModel("gpt-5.6-sol"); w != 1_050_000 {
		t.Fatalf("gpt-5.6-sol window = %d want 1050000", w)
	}
	if w := services.ContextWindowForModel("gpt-5.6-terra"); w != 1_050_000 {
		t.Fatalf("gpt-5.6-terra window = %d want 1050000", w)
	}
	if w := services.ContextWindowForModel("gpt-5.6-luna"); w != 1_050_000 {
		t.Fatalf("gpt-5.6-luna window = %d want 1050000", w)
	}
	if w := services.ContextWindowForModel("gemini-3.7-flash"); w != 1_048_576 {
		t.Fatalf("gemini-3.7-flash window = %d want 1048576", w)
	}
	if w := services.ContextWindowForModel("deepseek-v4-flash"); w != 1_000_000 {
		t.Fatalf("deepseek-v4-flash window = %d want 1000000", w)
	}
	if w := services.ContextWindowForModel("glm-5.3"); w != 1_048_576 {
		t.Fatalf("glm-5.3 window = %d want 1048576", w)
	}
	if w := services.ContextWindowForModel("kimi-k3"); w != 1_048_576 {
		t.Fatalf("kimi-k3 window = %d want 1048576", w)
	}
	if w := services.ContextWindowForModel("qwen3.8-max"); w != 1_000_000 {
		t.Fatalf("qwen3.8-max window = %d want 1000000", w)
	}
	if w := services.ContextWindowForModel("doubao-seed-2.1"); w != 256_000 {
		t.Fatalf("doubao-seed-2.1 window = %d want 256000", w)
	}
}

func TestContextWindowUnknownFallsBackToDefault(t *testing.T) {
	if w := services.ContextWindowForModel("stealth/ox-alpha"); w != services.DefaultContextWindow {
		t.Fatalf("unknown window = %d want %d", w, services.DefaultContextWindow)
	}
}

func TestThresholdForModel(t *testing.T) {
	want := int(float64(1_000_000) * services.DefaultCompactionRatio)
	if got := services.ThresholdForModel("claude-opus-5"); got != want {
		t.Fatalf("threshold = %d want %d", got, want)
	}
}

func TestResolveCompactionConfigPrecedence(t *testing.T) {
	// Model registry
	cfg := services.ResolveCompactionConfig("gpt-5.6-sol", 0, 0)
	if cfg.TokenThreshold != int(float64(1_050_000)*services.DefaultCompactionRatio) {
		t.Fatalf("model threshold = %d", cfg.TokenThreshold)
	}
	// overrideWindow beats model
	cfg = services.ResolveCompactionConfig("gpt-5.6-sol", 100_000, 0)
	if cfg.TokenThreshold != 80_000 {
		t.Fatalf("window override = %d want 80000", cfg.TokenThreshold)
	}
	// overrideThreshold beats everything
	cfg = services.ResolveCompactionConfig("gpt-5.6-sol", 100_000, 42_000)
	if cfg.TokenThreshold != 42_000 {
		t.Fatalf("threshold override = %d want 42000", cfg.TokenThreshold)
	}
}

func TestRegisterModelLimitExactMatch(t *testing.T) {
	services.RegisterModelLimit("my-custom-model", 500_000)
	// RegisterModelLimit stores in dynamicLimits (exact match via
	// contextWindowWithDynamic); ContextWindowForModel stays static.
	if w := services.ContextWindowForModel("gpt-5.6-sol"); w != 1_050_000 {
		t.Fatalf("after register, gpt-5.6-sol changed to %d", w)
	}
}
