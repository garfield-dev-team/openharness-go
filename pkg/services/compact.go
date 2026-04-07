package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openharness/openharness/pkg/types"
)

// CompactionConfig holds configuration for the compaction system.
type CompactionConfig struct {
	MinMessages        int
	TokenThreshold     int
	PreserveRecent     int
	MaxToolResultChars int
	SnipMaxChars       int
}

// DefaultCompactionConfig returns the default configuration.
func DefaultCompactionConfig() *CompactionConfig {
	return &CompactionConfig{
		MinMessages:        6,
		TokenThreshold:     40000,
		PreserveRecent:     4,
		MaxToolResultChars: 50000,
		SnipMaxChars:       8000,
	}
}

// EstimateTokens returns a rough token count for a string.
func EstimateTokens(text string) int {
	n := (len(text) + 3) / 4
	if n < 1 {
		n = 1
	}
	return n
}

// EstimateMessageTokens sums the estimated token count over a slice of messages.
func EstimateMessageTokens(messages []types.ConversationMessage) int {
	total := 0
	for _, m := range messages {
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				total += EstimateTokens(b.Text)
			case "tool_result":
				total += EstimateTokens(b.Content)
			case "tool_use":
				inputBytes, _ := json.Marshal(b.Input)
				total += EstimateTokens(b.Name) + EstimateTokens(string(inputBytes))
			}
		}
		total += 4
	}
	return total
}

// ShouldCompact checks whether compaction should be triggered.
func ShouldCompact(messages []types.ConversationMessage, config *CompactionConfig) bool {
	if len(messages) <= config.MinMessages {
		return false
	}
	compactableCount := len(messages) - config.PreserveRecent
	if compactableCount < 4 {
		return false
	}
	totalTokens := EstimateMessageTokens(messages)
	return totalTokens >= config.TokenThreshold
}

// RunPipeline runs the full 5-stage compaction pipeline.
// It returns the compacted message slice.
func RunPipeline(
	ctx context.Context,
	messages []types.ConversationMessage,
	config *CompactionConfig,
	collapseBuffer *[]types.ConversationMessage,
	summarizeFn func(context.Context, string) (string, error),
) ([]types.ConversationMessage, error) {

	// Make a deep-ish copy of the slice and its blocks so we don't mutate original history unless intended
	msgs := cloneMessages(messages)

	// L1: Truncate oversized tool results
	truncateToolResults(msgs, config.MaxToolResultChars)

	// L2: Snip old messages
	snipCompact(msgs, config)

	// L3: Microcompact
	microcompact(msgs)

	if !ShouldCompact(msgs, config) {
		return msgs, nil
	}

	// L4: Context Collapse — drain pre-staged collapsed messages
	if collapseBuffer != nil && len(*collapseBuffer) > 0 {
		drainCollapse(msgs, collapseBuffer)
		if !ShouldCompact(msgs, config) {
			return msgs, nil
		}
	}

	// L5: Auto-compact (LLM summary)
	compacted, err := autoCompact(ctx, msgs, config, summarizeFn)
	if err != nil {
		return msgs, err // fallback to L3 result
	}

	return compacted, nil
}

// cloneMessages copies the message slice and content blocks.
func cloneMessages(messages []types.ConversationMessage) []types.ConversationMessage {
	out := make([]types.ConversationMessage, len(messages))
	for i, m := range messages {
		out[i] = types.ConversationMessage{
			Role:    m.Role,
			Content: make([]types.ContentBlock, len(m.Content)),
		}
		copy(out[i].Content, m.Content)
	}
	return out
}

// L1: Truncate oversized tool results in-place.
func truncateToolResults(messages []types.ConversationMessage, maxChars int) {
	for i := range messages {
		for j, block := range messages[i].Content {
			if block.Type == "tool_result" && len(block.Content) > maxChars {
				tail := block.Content[len(block.Content)-maxChars:]
				removed := len(block.Content) - maxChars
				messages[i].Content[j].Content = fmt.Sprintf("[tool result truncated: %d chars removed]...%s", removed, tail)
			}
		}
	}
}

// L2: Snip old messages — keep first N chars and last N chars, replace middle.
func snipCompact(messages []types.ConversationMessage, config *CompactionConfig) {
	preserveStart := len(messages) - config.PreserveRecent
	if preserveStart <= 0 {
		return
	}

	halfBudget := config.SnipMaxChars / 2

	for i := 0; i < preserveStart; i++ {
		for j, block := range messages[i].Content {
			switch block.Type {
			case "text":
				if len(block.Text) > halfBudget*2 {
					head := block.Text[:halfBudget]
					tail := block.Text[len(block.Text)-halfBudget:]
					snipped := len(block.Text) - halfBudget*2
					messages[i].Content[j].Text = fmt.Sprintf("%s...\n[snipped %d chars]\n...%s", head, snipped, tail)
				}
			case "tool_result":
				if len(block.Content) > halfBudget*2 {
					head := block.Content[:halfBudget]
					tail := block.Content[len(block.Content)-halfBudget:]
					snipped := len(block.Content) - halfBudget*2
					messages[i].Content[j].Content = fmt.Sprintf("%s...\n[snipped %d chars]\n...%s", head, snipped, tail)
				}
			}
		}
	}
}

// L3: Microcompact — remove redundant whitespace.
func microcompact(messages []types.ConversationMessage) {
	for i := range messages {
		for j, block := range messages[i].Content {
			switch block.Type {
			case "text":
				messages[i].Content[j].Text = compactWhitespace(block.Text)
			case "tool_result":
				messages[i].Content[j].Content = compactWhitespace(block.Content)
			}
		}
	}
}

func compactWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	var result []string
	prevBlank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			if !prevBlank {
				result = append(result, "")
				prevBlank = true
			}
		} else {
			result = append(result, line)
			prevBlank = false
		}
	}
	return strings.Join(result, "\n")
}

// L4: Context Collapse
func drainCollapse(messages []types.ConversationMessage, collapseBuffer *[]types.ConversationMessage) {
	// Draining simply means clearing the buffer (permanent deletion).
	// The messages were already removed from the active list when staged.
	*collapseBuffer = nil
}

// StageForCollapse moves old messages from active list to collapse buffer.
func StageForCollapse(messages *[]types.ConversationMessage, collapseBuffer *[]types.ConversationMessage, preserveRecent int) {
	if len(*messages) <= preserveRecent+2 {
		return
	}
	drainCount := (len(*messages) - preserveRecent) / 2
	if drainCount > 0 {
		start := 0
		if len(*messages) > 0 {
			// preserve summary message if it exists
			for _, b := range (*messages)[0].Content {
				if b.Type == "text" && strings.Contains(b.Text, "[Conversation auto-compacted") {
					start = 1
					break
				}
			}
		}
		end := start + drainCount
		if end <= len(*messages) {
			staged := make([]types.ConversationMessage, end-start)
			copy(staged, (*messages)[start:end])
			*collapseBuffer = append(*collapseBuffer, staged...)

			// Remove from active
			*messages = append((*messages)[:start], (*messages)[end:]...)
		}
	}
}

// L5: Auto-compact
func autoCompact(
	ctx context.Context,
	messages []types.ConversationMessage,
	config *CompactionConfig,
	summarizeFn func(context.Context, string) (string, error),
) ([]types.ConversationMessage, error) {
	if summarizeFn == nil {
		return messages, nil // cannot do L5 without summarize function
	}

	preserveCount := config.PreserveRecent
	if len(messages) <= preserveCount {
		return messages, nil
	}

	splitPoint := len(messages) - preserveCount
	oldMessages := messages[:splitPoint]
	recentMessages := messages[splitPoint:]

	conversationText := formatMessagesForSummary(oldMessages)
	summaryPrompt := fmt.Sprintf(`Summarize the following conversation history into a structured summary. Preserve ALL key information needed to continue the work.

Format your response as:
<summary>
<scope>Overall project/task scope</scope>
<tools_used>Which tools were used and for what</tools_used>
<key_files>Important files mentioned (with paths, max 10)</key_files>
<current_work>What was being worked on most recently</current_work>
<pending_work>What still needs to be done</pending_work>
<key_decisions>Important decisions, findings, or error patterns</key_decisions>
<code_context>Any code snippets or patterns that must be preserved</code_context>
</summary>

Conversation to summarize:

%s`, conversationText)

	summaryText, err := summarizeFn(ctx, summaryPrompt)
	if err != nil {
		return nil, fmt.Errorf("compaction summary failed: %w", err)
	}

	summaryMsg := types.ConversationMessage{
		Role: "user",
		Content: []types.ContentBlock{
			types.NewTextBlock(fmt.Sprintf("[Conversation auto-compacted at turn %d. Summary below]\n\n%s", len(oldMessages), summaryText)),
		},
	}

	var newMessages []types.ConversationMessage
	newMessages = append(newMessages, summaryMsg)
	newMessages = append(newMessages, recentMessages...)

	return newMessages, nil
}

func formatMessagesForSummary(messages []types.ConversationMessage) string {
	var parts []string
	for _, msg := range messages {
		role := "User"
		if msg.Role == "assistant" {
			role = "Assistant"
		}

		var text string
		for _, b := range msg.Content {
			switch b.Type {
			case "text":
				text += b.Text + "\n"
			case "tool_result":
				text += fmt.Sprintf("[ToolResult]: %s\n", b.Content)
			case "tool_use":
				inputBytes, _ := json.Marshal(b.Input)
				text += fmt.Sprintf("[ToolUse: %s] %s\n", b.Name, string(inputBytes))
			}
		}

		if text != "" {
			if len(text) > 3000 {
				text = fmt.Sprintf("%s...[truncated %d chars]", text[:3000], len(text)-3000)
			}
			parts = append(parts, fmt.Sprintf("**%s**: %s", role, text))
		}
	}
	return strings.Join(parts, "\n\n---\n\n")
}
