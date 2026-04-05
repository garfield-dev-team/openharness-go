// Package services provides ancillary services such as context compaction
// and token estimation.
package services

import (
	"fmt"
	"strings"

	"github.com/openharness/openharness/pkg/types"
)

// EstimateTokens returns a rough token count for a string.
// Mirrors the Python heuristic: max(1, (len(text)+3)/4).
func EstimateTokens(text string) int {
	n := (len(text) + 3) / 4
	if n < 1 {
		n = 1
	}
	return n
}

// EstimateMessageTokens sums the estimated token count over a slice of messages.
func EstimateMessageTokens(messages []*types.ConversationMessage) int {
	total := 0
	for _, m := range messages {
		total += EstimateTokens(m.GetText())
		// Small overhead per message for role + structure.
		total += 4
	}
	return total
}

// CompactMessages removes older messages while keeping the first (system
// context) message and the most recent keepRecent messages.
func CompactMessages(messages []*types.ConversationMessage, keepRecent int) []*types.ConversationMessage {
	if len(messages) <= keepRecent+1 {
		return messages
	}
	// Keep first message (system context setup) + last keepRecent.
	result := make([]*types.ConversationMessage, 0, keepRecent+2)
	result = append(result, messages[0])

	// Insert a synthetic summary message.
	dropped := messages[1 : len(messages)-keepRecent]
	summary := SummarizeMessages(dropped)
	summaryMsg := &types.ConversationMessage{
		Role:    "user",
		Content: []types.ContentBlock{types.NewTextBlock("[Earlier conversation compacted]\n" + summary)},
	}
	result = append(result, summaryMsg)

	// Append the recent messages.
	result = append(result, messages[len(messages)-keepRecent:]...)
	return result
}

// SummarizeMessages produces a brief textual summary of a slice of messages.
// A production implementation would call an LLM; this version creates a
// deterministic digest.
func SummarizeMessages(messages []*types.ConversationMessage) string {
	if len(messages) == 0 {
		return "(no messages)"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Summary of %d earlier messages:\n", len(messages)))
	for i, m := range messages {
		text := m.GetText()
		if len(text) > 120 {
			text = text[:120] + "..."
		}
		sb.WriteString(fmt.Sprintf("  [%d] %s: %s\n", i+1, m.Role, text))
		if i >= 9 {
			sb.WriteString(fmt.Sprintf("  ... and %d more messages\n", len(messages)-10))
			break
		}
	}
	return sb.String()
}
