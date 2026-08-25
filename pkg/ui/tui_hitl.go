package ui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// tuiHITL routes human-in-the-loop questions and permission prompts
// through the Bubble Tea overlay instead of raw stdin/stdout.
type tuiHITL struct {
	p *tea.Program
}

func newTUIHITL() *tuiHITL { return &tuiHITL{} }

func (h *tuiHITL) bind(p *tea.Program) { h.p = p }

// AskUser shows the question as an in-TUI overlay. With options, the
// numeric choice maps to the option; free text passes through as-is.
func (h *tuiHITL) AskUser(ctx context.Context, question string, options []string) (string, error) {
	return h.ask(ctx, hitlPromptMsg{question: question, options: options})
}

// AskPermission shows the permission request with allow/deny options.
func (h *tuiHITL) AskPermission(ctx context.Context, toolName, reason string) (bool, error) {
	q := fmt.Sprintf("Allow %s? (%s)", toolName, reason)
	ans, err := h.ask(ctx, hitlPromptMsg{question: q, options: []string{"allow", "deny"}})
	if err != nil {
		return false, err
	}
	return ans == "allow", nil
}

func (h *tuiHITL) ask(ctx context.Context, msg hitlPromptMsg) (string, error) {
	if h.p == nil {
		return "", fmt.Errorf("tui: program not bound")
	}
	msg.reply = make(chan string, 1)
	h.p.Send(msg)
	select {
	case ans := <-msg.reply:
		return ans, nil
	case <-ctx.Done():
		h.p.Send(struct{}{}) // nudge a repaint; Update ignores unknown msgs
		return "", ctx.Err()
	}
}
