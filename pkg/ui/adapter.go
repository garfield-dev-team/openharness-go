package ui

import (
	"context"

	"github.com/openharness/openharness/pkg/api"
	"github.com/openharness/openharness/pkg/engine"
	"github.com/openharness/openharness/pkg/types"
)

// apiClientAdapter adapts api.AnthropicApiClient to engine.StreamingLLMClient.
type apiClientAdapter struct {
	client api.MessageStreamer
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
		for ev := range apiCh {
			if ev.Err != nil {
				outCh <- engine.LLMStreamEvent{Err: ev.Err}
				continue
			}
			if ev.TextDelta != nil {
				outCh <- engine.LLMStreamEvent{TextDelta: ev.TextDelta.Text}
			}
			if ev.MessageComplete != nil {
				msg := ev.MessageComplete.Message
				// Merge the raw string for backward compatibility or let engine use blocks
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
