package services

import "strings"

// DefaultContextWindow is used when a model is not in the known table.
const DefaultContextWindow = 200_000

// DefaultCompactionRatio is the fraction of the context window at which
// compaction triggers (e.g. 0.8 of 200k = 160k tokens).
const DefaultCompactionRatio = 0.8

var knownLimits = []struct {
	prefix string
	window int
}{
	// Anthropic — exact generation (user-filtered, 1M tier)
	{"claude-opus-5", 1_000_000},
	{"claude-sonnet-5", 1_000_000},
	{"claude-fable-5", 1_000_000},
	// OpenAI GPT-5.6 family — 1,050,000 (exact API value)
	{"gpt-5.6-sol", 1_050_000},
	{"gpt-5.6-terra", 1_050_000},
	{"gpt-5.6-luna", 1_050_000},
	// Others — exact values from latest provider docs
	{"muse-spark", 1_048_576},
	{"gemini-3.7-flash", 1_048_576},
	{"deepseek-v4-flash", 1_000_000},
	{"deepseek-v4-pro", 1_000_000},
	{"glm-5.3", 1_048_576},
	{"kimi-k3", 1_048_576},
	{"qwen3.8-max", 1_000_000},
	{"doubao-seed-2.1", 256_000},
}

// ModelInfo describes a selectable model for UI pickers.
type ModelInfo struct {
	ID     string `json:"id"`
	Window int    `json:"window"`
	Label  string `json:"label"`
}

// ListKnownModels returns the curated model catalogue (stable order).
func ListKnownModels() []ModelInfo {
	out := make([]ModelInfo, 0, len(knownLimits))
	for _, kv := range knownLimits {
		out = append(out, ModelInfo{ID: kv.prefix, Window: kv.window, Label: kv.prefix})
	}
	return out
}

// IsKnownModel reports whether model is in the curated catalogue (case-insensitive).
func IsKnownModel(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	for _, kv := range knownLimits {
		if lower == kv.prefix {
			return true
		}
	}
	return false
}

// ContextWindowForModel returns the context window for a model identifier.
// The match is case-insensitive prefix; unknown models return DefaultContextWindow.
func ContextWindowForModel(model string) int {
	lower := strings.ToLower(strings.TrimSpace(model))
	for _, kv := range knownLimits {
		if strings.HasPrefix(lower, kv.prefix) {
			return kv.window
		}
		// Also match anywhere in the model string (e.g. "stealth/ox-alpha" is
		// not in the table so it falls through to default; a provider like
		// "openrouter/deepseek/deepseek-chat" contains "deepseek").
		if strings.Contains(lower, kv.prefix) {
			return kv.window
		}
	}
	return DefaultContextWindow
}

// ThresholdForModel returns the compaction token threshold for a model
// (context window * DefaultCompactionRatio).
func ThresholdForModel(model string) int {
	return int(float64(ContextWindowForModel(model)) * DefaultCompactionRatio)
}

// ResolveCompactionConfig builds a CompactionConfig for the given model.
// Precedence: overrideThreshold > overrideWindow*ratio > model registry.
func ResolveCompactionConfig(model string, overrideWindow, overrideThreshold int) *CompactionConfig {
	base := DefaultCompactionConfig()
	if overrideThreshold > 0 {
		base.TokenThreshold = overrideThreshold
		return base
	}
	if overrideWindow > 0 {
		base.TokenThreshold = int(float64(overrideWindow) * DefaultCompactionRatio)
		return base
	}
	base.TokenThreshold = ThresholdForModel(model)
	return base
}

// FetchLimits is a hook for dynamic provider-based discovery. Providers that
// expose a models endpoint (e.g. OpenRouter GET /api/v1/models with
// context_length) can call RegisterModelLimit to extend the table at runtime.
var dynamicLimits = map[string]int{}

// RegisterModelLimit adds or overrides a context window for a model id
// (exact match, case-insensitive). Call this after a successful fetch from
// a provider's models API.
func RegisterModelLimit(model string, window int) {
	if window <= 0 {
		return
	}
	dynamicLimits[strings.ToLower(strings.TrimSpace(model))] = window
}

// contextWindowWithDynamic checks dynamicLimits first (exact match), then
// falls back to the static prefix table.
func contextWindowWithDynamic(model string) int {
	if w, ok := dynamicLimits[strings.ToLower(strings.TrimSpace(model))]; ok {
		return w
	}
	return ContextWindowForModel(model)
}
