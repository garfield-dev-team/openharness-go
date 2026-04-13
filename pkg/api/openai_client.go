package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openharness/openharness/pkg/types"
)

const defaultOpenAIBaseURL = "https://api.openai.com/v1"

// OpenAIApiClient is a wrapper around net/http that calls OpenAI-compatible
// Chat Completions API with retry logic and SSE streaming.
type OpenAIApiClient struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewOpenAIApiClient creates a new OpenAI API client.
func NewOpenAIApiClient(apiKey string, baseURL string) *OpenAIApiClient {
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &OpenAIApiClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 10 * time.Minute},
	}
}

// StreamMessage yields streamed events for the given request.
func (c *OpenAIApiClient) StreamMessage(ctx context.Context, req *ApiMessageRequest) (<-chan ApiStreamEvent, error) {
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
			log.Printf("OpenAI API request failed (attempt %d/%d, status=%s), retrying in %.1fs: %v",
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

// ---------------------------------------------------------------------------
// OpenAI Format Converters
// ---------------------------------------------------------------------------

type openaiMessage struct {
	Role             string           `json:"role"`
	Content          string           `json:"content,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	Name             string           `json:"name,omitempty"`
}

type openaiToolCall struct {
	Index    int                `json:"index,omitempty"`
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function openaiFunctionCall `json:"function"`
}

type openaiFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiTool struct {
	Type     string         `json:"type"`
	Function openaiFunction `json:"function"`
}

type openaiFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type openaiChatRequest struct {
	Model     string          `json:"model"`
	Messages  []openaiMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens,omitempty"`
	Stream    bool            `json:"stream"`
	Tools     []openaiTool    `json:"tools,omitempty"`
}

func (c *OpenAIApiClient) buildRequestBody(req *ApiMessageRequest) ([]byte, error) {
	var messages []openaiMessage

	if req.SystemPrompt != nil && *req.SystemPrompt != "" {
		messages = append(messages, openaiMessage{
			Role:    "system",
			Content: *req.SystemPrompt,
		})
	}

	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			var contentBuilder strings.Builder
			var hasToolResult bool
			for _, b := range m.Content {
				switch b.Type {
				case "text":
					contentBuilder.WriteString(b.Text)
					contentBuilder.WriteString("\n")
				case "tool_result":
					// For OpenAI, tool_result is a separate message with role "tool"
					// We must append any accumulated text as a user message first
					if contentBuilder.Len() > 0 {
						messages = append(messages, openaiMessage{
							Role:    "user",
							Content: strings.TrimSuffix(contentBuilder.String(), "\n"),
						})
						contentBuilder.Reset()
					}
					messages = append(messages, openaiMessage{
						Role:       "tool",
						Content:    b.Content,
						ToolCallID: b.ToolUseID,
					})
					hasToolResult = true
				}
			}
			if contentBuilder.Len() > 0 {
				messages = append(messages, openaiMessage{
					Role:    "user",
					Content: strings.TrimSuffix(contentBuilder.String(), "\n"),
				})
			} else if !hasToolResult {
				// Empty user message?
				messages = append(messages, openaiMessage{
					Role:    "user",
					Content: "",
				})
			}
		case "assistant":
			var contentBuilder strings.Builder
			var toolCalls []openaiToolCall
			for _, b := range m.Content {
				switch b.Type {
				case "text":
					contentBuilder.WriteString(b.Text)
					contentBuilder.WriteString("\n")
				case "tool_use":
					args, _ := json.Marshal(b.Input)
					toolCalls = append(toolCalls, openaiToolCall{
						ID:   b.ID,
						Type: "function",
						Function: openaiFunctionCall{
							Name:      b.Name,
							Arguments: string(args),
						},
					})
				}
			}
			msg := openaiMessage{
				Role:             "assistant",
				Content:          strings.TrimSuffix(contentBuilder.String(), "\n"),
				ReasoningContent: m.ReasoningContent,
			}
			if len(toolCalls) > 0 {
				msg.ToolCalls = toolCalls
			}
			messages = append(messages, msg)
		}
	}

	var tools []openaiTool
	for _, t := range req.Tools {
		name, _ := t["name"].(string)
		desc, _ := t["description"].(string)
		schema, _ := t["input_schema"].(map[string]any)

		tools = append(tools, openaiTool{
			Type: "function",
			Function: openaiFunction{
				Name:        name,
				Description: desc,
				Parameters:  schema,
			},
		})
	}

	oReq := openaiChatRequest{
		Model:     req.Model,
		Messages:  messages,
		MaxTokens: req.MaxTokens,
		Stream:    true,
	}
	if len(tools) > 0 {
		oReq.Tools = tools
	}

	return json.Marshal(oReq)
}

// ---------------------------------------------------------------------------
// Single-attempt streaming (SSE)
// ---------------------------------------------------------------------------

type openaiStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string           `json:"content"`
			ReasoningContent string           `json:"reasoning_content"`
			ToolCalls        []openaiToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (c *OpenAIApiClient) streamOnce(
	ctx context.Context,
	req *ApiMessageRequest,
	events chan<- ApiStreamEvent,
) error {
	body, err := c.buildRequestBody(req)
	if err != nil {
		return fmt.Errorf("build request body: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return &apiHTTPError{
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
			Headers:    resp.Header,
		}
	}

	scanner := bufio.NewScanner(resp.Body)
	var fullTextBuilder strings.Builder
	var reasoningBuilder strings.Builder
	toolCallBuilders := make(map[int]*openaiToolCallBuilder)
	var stopReason string
	var usage types.UsageSnapshot

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk openaiStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.Usage != nil {
			usage.InputTokens = chunk.Usage.PromptTokens
			usage.OutputTokens = chunk.Usage.CompletionTokens
		}

		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			if choice.FinishReason != "" && choice.FinishReason != "null" {
				stopReason = choice.FinishReason
			}

			if choice.Delta.ReasoningContent != "" {
				reasoningBuilder.WriteString(choice.Delta.ReasoningContent)
			}

			if choice.Delta.Content != "" {
				fullTextBuilder.WriteString(choice.Delta.Content)
				events <- ApiStreamEvent{
					TextDelta: &ApiTextDeltaEvent{
						Text: choice.Delta.Content,
					},
				}
			}

			for _, tc := range choice.Delta.ToolCalls {
				builder, ok := toolCallBuilders[tc.Index]
				if !ok {
					builder = &openaiToolCallBuilder{}
					toolCallBuilders[tc.Index] = builder
				}
				if tc.ID != "" {
					builder.id = tc.ID
				}
				if tc.Function.Name != "" {
					builder.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					builder.arguments.WriteString(tc.Function.Arguments)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read sse stream: %w", err)
	}

	finalMsg := types.ConversationMessage{Role: "assistant"}
	if reasoningBuilder.Len() > 0 {
		finalMsg.ReasoningContent = reasoningBuilder.String()
	}
	if fullTextBuilder.Len() > 0 {
		finalMsg.Content = append(finalMsg.Content, types.NewTextBlock(fullTextBuilder.String()))
	}
	for i := 0; i < len(toolCallBuilders); i++ {
		builder := toolCallBuilders[i]
		var input map[string]any
		if builder.arguments.Len() > 0 {
			_ = json.Unmarshal([]byte(builder.arguments.String()), &input)
		}
		if input == nil {
			input = make(map[string]any)
		}

		id := builder.id
		if id == "" {
			id = fmt.Sprintf("call_%d", time.Now().UnixNano())
		}
		finalMsg.Content = append(finalMsg.Content, types.ContentBlock{
			Type:  "tool_use",
			ID:    id,
			Name:  builder.name,
			Input: input,
		})
	}

	events <- ApiStreamEvent{
		MessageComplete: &ApiMessageCompleteEvent{
			Message:    finalMsg,
			Usage:      usage,
			StopReason: stopReason,
		},
	}

	return nil
}

type openaiToolCallBuilder struct {
	id        string
	name      string
	arguments strings.Builder
}
