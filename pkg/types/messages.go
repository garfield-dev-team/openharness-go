package types

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openharness/openharness/pkg/types/internal/uid"
)

// ---------------------------------------------------------------------------
// ContentBlock – a tagged union over text / tool_use / tool_result.
// ---------------------------------------------------------------------------

// ContentBlock is a tagged union. Only the fields relevant to the Type are populated.
type ContentBlock struct {
	Type string `json:"type"`

	// Text fields (type == "text")
	Text string `json:"text,omitempty"`

	// ToolUse fields (type == "tool_use")
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`

	// ToolResult fields (type == "tool_result")
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// NewTextBlock creates a text content block.
func NewTextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// NewToolUseBlock creates a tool_use content block with a generated ID.
func NewToolUseBlock(name string, input map[string]any) ContentBlock {
	return ContentBlock{
		Type:  "tool_use",
		ID:    fmt.Sprintf("toolu_%s", uid.NewHex()),
		Name:  name,
		Input: input,
	}
}

// NewToolResultBlock creates a tool_result content block.
func NewToolResultBlock(toolUseID, content string, isError bool) ContentBlock {
	return ContentBlock{
		Type:      "tool_result",
		ToolUseID: toolUseID,
		Content:   content,
		IsError:   isError,
	}
}

// ---------------------------------------------------------------------------
// ConversationMessage
// ---------------------------------------------------------------------------

// ConversationMessage represents a single assistant or user message.
type ConversationMessage struct {
	Role    string         `json:"role"` // "user" or "assistant"
	Content []ContentBlock `json:"content"`
}

// FromUserText constructs a user message from raw text.
func FromUserText(text string) ConversationMessage {
	return ConversationMessage{
		Role:    "user",
		Content: []ContentBlock{NewTextBlock(text)},
	}
}

// GetText returns the concatenated text blocks.
func (m ConversationMessage) GetText() string {
	var parts []string
	for _, b := range m.Content {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// ToolUses returns all tool_use blocks in the message.
func (m ConversationMessage) ToolUses() []ContentBlock {
	var out []ContentBlock
	for _, b := range m.Content {
		if b.Type == "tool_use" {
			out = append(out, b)
		}
	}
	return out
}

// ToAPIParam converts the message into the Anthropic API wire format.
func (m ConversationMessage) ToAPIParam() map[string]any {
	blocks := make([]map[string]any, 0, len(m.Content))
	for _, b := range m.Content {
		blocks = append(blocks, serializeContentBlock(b))
	}
	return map[string]any{
		"role":    m.Role,
		"content": blocks,
	}
}

func serializeContentBlock(b ContentBlock) map[string]any {
	switch b.Type {
	case "text":
		return map[string]any{"type": "text", "text": b.Text}
	case "tool_use":
		return map[string]any{
			"type":  "tool_use",
			"id":    b.ID,
			"name":  b.Name,
			"input": b.Input,
		}
	case "tool_result":
		return map[string]any{
			"type":        "tool_result",
			"tool_use_id": b.ToolUseID,
			"content":     b.Content,
			"is_error":    b.IsError,
		}
	default:
		return map[string]any{"type": b.Type}
	}
}

// AssistantMessageFromAPI converts a raw API response JSON into a ConversationMessage.
func AssistantMessageFromAPI(raw json.RawMessage) (ConversationMessage, error) {
	var resp struct {
		Content []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text,omitempty"`
			ID    string         `json:"id,omitempty"`
			Name  string         `json:"name,omitempty"`
			Input map[string]any `json:"input,omitempty"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ConversationMessage{}, fmt.Errorf("parse assistant message: %w", err)
	}

	msg := ConversationMessage{Role: "assistant"}
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			msg.Content = append(msg.Content, NewTextBlock(block.Text))
		case "tool_use":
			id := block.ID
			if id == "" {
				id = fmt.Sprintf("toolu_%s", uid.NewHex())
			}
			msg.Content = append(msg.Content, ContentBlock{
				Type:  "tool_use",
				ID:    id,
				Name:  block.Name,
				Input: block.Input,
			})
		}
	}
	return msg, nil
}
