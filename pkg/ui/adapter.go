package ui

import (
	"context"

	"github.com/openharness/openharness/pkg/api"
	"github.com/openharness/openharness/pkg/engine"
	"github.com/openharness/openharness/pkg/types"
)

// apiClientAdapter adapts api.AnthropicApiClient to engine.StreamingLLMClient.
type apiClientAdapter struct {
	client *api.AnthropicApiClient
	model  string
}

// StreamMessage implements engine.StreamingLLMClient.
func (a *apiClientAdapter) StreamMessage(ctx context.Context, params engine.LLMRequestParams) (<-chan engine.LLMStreamEvent, error) {
	sysPrompt := params.SystemPrompt
	req := &api.ApiMessageRequest{
		Model:        params.Model,
		Messages:     params.Messages,
		SystemPrompt: &sysPrompt,
		MaxTokens:    params.MaxTokens,
		Tools:        params.Tools,
	}

	apiCh, err := a.client.StreamMessage(ctx, req)
	if err != nil {
		return nil, err
	}

	outCh := make(chan engine.LLMStreamEvent, 64)
	go func() {
		defer close(outCh)
		var fullText string
		for ev := range apiCh {
			if ev.TextDelta != nil {
				fullText += ev.TextDelta.Text
				outCh <- engine.LLMStreamEvent{TextDelta: ev.TextDelta.Text}
			}
			if ev.MessageComplete != nil {
				msg := types.ConversationMessage{
					Role:    "assistant",
					Content: []types.ContentBlock{types.NewTextBlock(fullText)},
				}
				usage := &types.UsageSnapshot{
					OutputTokens: ev.MessageComplete.Usage.OutputTokens,
				}
				outCh <- engine.LLMStreamEvent{
					Message: &msg,
					Usage:   usage,
				}
			}
		}
	}()

	return outCh, nil
}
