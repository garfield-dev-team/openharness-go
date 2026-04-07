package api

import (
	"strings"

	"github.com/openharness/openharness/pkg/config"
)

// ProviderInfo holds resolved provider metadata for UI and diagnostics.
type ProviderInfo struct {
	Name           string
	AuthKind       string
	VoiceSupported bool
	VoiceReason    string
}

// DetectProvider infers the active provider from the current settings.
func DetectProvider(s config.Settings) ProviderInfo {
	if s.Provider != "" {
		providerName := strings.ToLower(s.Provider)
		authKind := "api_key"
		voiceReason := "voice mode currently requires a dedicated Claude.ai-style provider"
		
		if strings.Contains(providerName, "bedrock") {
			authKind = "aws"
			voiceReason = "voice mode is not wired for Bedrock in this build"
		} else if strings.Contains(providerName, "vertex") || strings.Contains(providerName, "gcp") {
			authKind = "gcp"
			voiceReason = "voice mode is not wired for Vertex in this build"
		}

		return ProviderInfo{
			Name:           providerName,
			AuthKind:       authKind,
			VoiceSupported: false,
			VoiceReason:    voiceReason,
		}
	}

	baseURL := strings.ToLower(derefString(s.BaseURL))
	model := strings.ToLower(s.Model)

	if strings.Contains(baseURL, "moonshot") || strings.HasPrefix(model, "kimi") {
		return ProviderInfo{Name: "moonshot-anthropic-compatible", AuthKind: "api_key", VoiceReason: "voice mode requires a Claude.ai-style authenticated voice backend"}
	}
	if strings.Contains(baseURL, "openai") || strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3") || strings.Contains(baseURL, "deepseek") {
		return ProviderInfo{Name: "openai-compatible", AuthKind: "api_key", VoiceReason: "voice mode currently requires a dedicated Claude.ai-style provider"}
	}
	if strings.Contains(baseURL, "bedrock") {
		return ProviderInfo{Name: "bedrock-compatible", AuthKind: "aws", VoiceReason: "voice mode is not wired for Bedrock in this build"}
	}
	if strings.Contains(baseURL, "vertex") || strings.Contains(baseURL, "aiplatform") {
		return ProviderInfo{Name: "vertex-compatible", AuthKind: "gcp", VoiceReason: "voice mode is not wired for Vertex in this build"}
	}
	if baseURL != "" {
		return ProviderInfo{Name: "anthropic-compatible", AuthKind: "api_key", VoiceReason: "voice mode currently requires a dedicated Claude.ai-style provider"}
	}
	return ProviderInfo{Name: "anthropic", AuthKind: "api_key", VoiceReason: "voice mode shell exists, but live voice auth/streaming is not configured in this build"}
}

// AuthStatus returns a compact auth status string.
func AuthStatus(s config.Settings) string {
	if s.APIKey != "" {
		return "configured"
	}
	return "missing"
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
