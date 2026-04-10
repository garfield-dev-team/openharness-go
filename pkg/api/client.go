// Package api provides the Anthropic API client with streaming and retry logic.
package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openharness/openharness/pkg/types"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	MaxRetries     = 3
	BaseDelay      = 1.0
	MaxDelay       = 30.0
	defaultBaseURL = "https://api.anthropic.com"
	messagesPath   = "/v1/messages"
	apiVersion     = "2023-06-01"
)

var retryableStatusCodes = map[int]bool{
	429: true, 500: true, 502: true, 503: true, 529: true,
}

// ---------------------------------------------------------------------------
// Request / event types
// ---------------------------------------------------------------------------

// ApiMessageRequest contains the input parameters for a model invocation.
type ApiMessageRequest struct {
	Model        string                        `json:"model"`
	Messages     []types.ConversationMessage   `json:"messages"`
	SystemPrompt *string                       `json:"system_prompt,omitempty"`
	MaxTokens    int                           `json:"max_tokens"`
	Tools        []map[string]any              `json:"tools,omitempty"`
}

// ApiStreamEvent is the union type for streaming events.
type ApiStreamEvent struct {
	TextDelta       *ApiTextDeltaEvent
	MessageComplete *ApiMessageCompleteEvent
	Err             error
}

// ApiTextDeltaEvent carries incremental text produced by the model.
type ApiTextDeltaEvent struct {
	Text string
}

// ApiMessageCompleteEvent is the terminal event containing the full assistant message.
type ApiMessageCompleteEvent struct {
	Message    types.ConversationMessage
	Usage      types.UsageSnapshot
	StopReason string
}

// ---------------------------------------------------------------------------
// MessageStreamer interface
// ---------------------------------------------------------------------------

// MessageStreamer is implemented by any client that can stream Anthropic-style
// message completions.
type MessageStreamer interface {
	StreamMessage(ctx context.Context, req *ApiMessageRequest) (<-chan ApiStreamEvent, error)
}

// ---------------------------------------------------------------------------
// AnthropicApiClient
// ---------------------------------------------------------------------------

// AnthropicApiClient is a thin wrapper around net/http that calls the Anthropic
// Messages API with retry logic and SSE streaming.
type AnthropicApiClient struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewAnthropicApiClient creates a new API client.
func NewAnthropicApiClient(apiKey string, baseURL string) *AnthropicApiClient {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &AnthropicApiClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 10 * time.Minute},
	}
}

// StreamMessage yields streamed events for the given request.
func (c *AnthropicApiClient) StreamMessage(ctx context.Context, req *ApiMessageRequest) (<-chan ApiStreamEvent, error) {
	events := make(chan ApiStreamEvent, 64)

	go func() {
		defer close(events)

		var lastErr error
		for attempt := 0; attempt <= MaxRetries; attempt++ {
			err := c.streamOnce(ctx, req, events)
			if err == nil {
				return
			}
			if _, ok := err.(types.OpenHarnessApiError); ok {
				sendError(events, err)
				return
			}
			lastErr = err
			if attempt >= MaxRetries || !isRetryable(err) {
				sendError(events, translateError(err))
				return
			}
			delay := getRetryDelay(attempt, err)
			statusStr := "?"
			if ae, ok := err.(*apiHTTPError); ok {
				statusStr = strconv.Itoa(ae.StatusCode)
			}
			log.Printf("API request failed (attempt %d/%d, status=%s), retrying in %.1fs: %v",
				attempt+1, MaxRetries+1, statusStr, delay.Seconds(), err)
			select {
			case <-ctx.Done():
				sendError(events, ctx.Err())
				return
			case <-time.After(delay):
			}
		}
		if lastErr != nil {
			sendError(events, translateError(lastErr))
		}
	}()

	return events, nil
}

func sendError(ch chan<- ApiStreamEvent, err error) {
	ch <- ApiStreamEvent{
		Err: err,
	}
}

// ---------------------------------------------------------------------------
// apiHTTPError
// ---------------------------------------------------------------------------

type apiHTTPError struct {
	StatusCode int
	Body       string
	Headers    http.Header
}

func (e *apiHTTPError) Error() string {
	return fmt.Sprintf("Anthropic API error %d: %s", e.StatusCode, e.Body)
}

// ---------------------------------------------------------------------------
// Single-attempt streaming (SSE)
// ---------------------------------------------------------------------------

func (c *AnthropicApiClient) streamOnce(
	ctx context.Context,
	req *ApiMessageRequest,
	events chan<- ApiStreamEvent,
) error {
	body, err := buildRequestBody(req)
	if err != nil {
		return fmt.Errorf("build request body: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+messagesPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Api-Key", c.apiKey)
	httpReq.Header.Set("Anthropic-Version", apiVersion)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return &apiHTTPError{
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
			Headers:    resp.Header,
		}
	}

	return parseSSEStream(resp.Body, events)
}

func buildRequestBody(req *ApiMessageRequest) ([]byte, error) {
	apiMessages := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		apiMessages = append(apiMessages, m.ToAPIParam())
	}

	payload := map[string]any{
		"model":      req.Model,
		"messages":   apiMessages,
		"max_tokens": req.MaxTokens,
		"stream":     true,
	}
	if req.SystemPrompt != nil && *req.SystemPrompt != "" {
		payload["system"] = *req.SystemPrompt
	}
	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
	}
	return json.Marshal(payload)
}

// ---------------------------------------------------------------------------
// SSE parser
// ---------------------------------------------------------------------------

type sseEvent struct {
	Event string
	Data  string
}

func parseSSEStream(r io.Reader, events chan<- ApiStreamEvent) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var current sseEvent

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			if current.Event != "" || current.Data != "" {
				if err := dispatchSSEEvent(current, events); err != nil {
					return err
				}
				current = sseEvent{}
			}
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			current.Event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			current.Data = strings.TrimPrefix(line, "data: ")
		} else if line == "data:" {
			current.Data = ""
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("sse scan: %w", err)
	}

	if current.Event != "" || current.Data != "" {
		if err := dispatchSSEEvent(current, events); err != nil {
			return err
		}
	}
	return nil
}

func dispatchSSEEvent(sse sseEvent, events chan<- ApiStreamEvent) error {
	switch sse.Event {
	case "content_block_delta":
		return handleContentBlockDelta(sse.Data, events)
	case "message_delta":
		return handleMessageDelta(sse.Data, events)
	case "error":
		return handleSSEError(sse.Data)
	case "message_stop", "message_start", "content_block_start", "content_block_stop", "ping":
		return nil
	default:
		return nil
	}
}

func handleContentBlockDelta(data string, events chan<- ApiStreamEvent) error {
	var payload struct {
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return fmt.Errorf("parse content_block_delta: %w", err)
	}
	if payload.Delta.Type != "text_delta" {
		return nil
	}
	if payload.Delta.Text != "" {
		events <- ApiStreamEvent{
			TextDelta: &ApiTextDeltaEvent{Text: payload.Delta.Text},
		}
	}
	return nil
}

func handleMessageDelta(data string, events chan<- ApiStreamEvent) error {
	var payload struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return fmt.Errorf("parse message_delta: %w", err)
	}
	events <- ApiStreamEvent{
		MessageComplete: &ApiMessageCompleteEvent{
			Message: types.ConversationMessage{Role: "assistant"},
			Usage: types.UsageSnapshot{
				OutputTokens: payload.Usage.OutputTokens,
			},
			StopReason: payload.Delta.StopReason,
		},
	}
	return nil
}

func handleSSEError(data string) error {
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return fmt.Errorf("API stream error: %s", data)
	}
	return fmt.Errorf("API stream error (%s): %s", payload.Error.Type, payload.Error.Message)
}

// ---------------------------------------------------------------------------
// Retry helpers
// ---------------------------------------------------------------------------

func isRetryable(err error) bool {
	if ae, ok := err.(*apiHTTPError); ok {
		return retryableStatusCodes[ae.StatusCode]
	}
	return isNetworkError(err)
}

func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, kw := range []string{"connection refused", "connection reset", "timeout", "no such host", "i/o timeout", "eof"} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

func getRetryDelay(attempt int, err error) time.Duration {
	if ae, ok := err.(*apiHTTPError); ok && ae.Headers != nil {
		if val := ae.Headers.Get("Retry-After"); val != "" {
			if secs, parseErr := strconv.ParseFloat(val, 64); parseErr == nil {
				return time.Duration(math.Min(secs, MaxDelay) * float64(time.Second))
			}
		}
	}
	delay := math.Min(BaseDelay*math.Pow(2, float64(attempt)), MaxDelay)
	jitter := rand.Float64() * delay * 0.25
	return time.Duration((delay + jitter) * float64(time.Second))
}

func translateError(err error) error {
	ae, ok := err.(*apiHTTPError)
	if !ok {
		return &types.RequestFailure{Message: err.Error()}
	}
	switch {
	case ae.StatusCode == 401 || ae.StatusCode == 403:
		return &types.AuthenticationFailure{Message: ae.Error()}
	case ae.StatusCode == 429:
		return &types.RateLimitFailure{Message: ae.Error()}
	default:
		return &types.RequestFailure{Message: ae.Error()}
	}
}
