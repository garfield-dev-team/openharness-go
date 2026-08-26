package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/openharness/openharness/pkg/engine"
	"github.com/openharness/openharness/pkg/services"
)

var (
	tuiTitleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	tuiDimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	tuiUserStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	tuiTextStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("15")) // assistant body text
	// tuiTextBlock frames assistant body with a cyan left bar so prose is
	// visually distinct from dim reasoning and orange tool lines.
	tuiTextBlock = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(lipgloss.Color("39")).
			PaddingLeft(1)
	tuiReasonStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	tuiToolStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // running tool line
	// Tool lines render in place: ◇ name args… while running, then the
	// same row flips to ✔ (green) or ✖ (red) on completion.
	tuiToolNameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	tuiOkStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	tuiErrStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	tuiWarnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
)

// slashCmd drives both the autocomplete menu and dispatch.
type slashCmd struct{ cmd, desc string }

var slashCommands = []slashCmd{
	{"/clear", "clear conversation history"},
	{"/compact", "compact conversation context now"},
	{"/cost", "show token usage"},
	{"/model", "switch model (interactive picker)"},
	{"/models", "list available models"},
	{"/help", "show help"},
	{"/exit", "exit the REPL"},
}

type tuiMode int

const (
	modeInput tuiMode = iota
	modeModelPicker
	modeHitl
)

// streamEventMsg forwards one engine event into Update so all state
// mutation happens on the UI goroutine (Elm style).
type streamEventMsg struct{ ev engine.StreamEventWithUsage }

type queryDoneMsg struct{ err error }

// hitlPromptMsg opens an interactive question/permission overlay. The
// reply channel receives the raw answer string.
type hitlPromptMsg struct {
	question string
	options  []string
	reply    chan string
}

type statusMsg struct{ text string }

// compactDoneMsg reports the result of a manual /compact run.
type compactDoneMsg struct {
	before, after int
	err           error
}

// tuiModel is the Bubble Tea replacement for the line-based REPL.
type tuiModel struct {
	rt  *RuntimeBundle
	p   *tea.Program
	ctx context.Context

	input    textinput.Model
	spin     spinner.Model
	viewport viewport.Model

	lines          []string // settled transcript blocks (styled)
	cur            strings.Builder
	curIsReasoning bool
	busy           bool
	autoFollow     bool // pinned to bottom; disabled by manual scrolling
	awaitingFirst  bool // query in flight, no token received yet (TTFT)
	openTools      []int // indices of running tool lines, replaced on completion

	mode     tuiMode
	models   []services.ModelInfo
	cursor   int
	hitl     *hitlPromptMsg
	slashIdx int // highlighted row in the slash command menu

	width, height int
	status        string
}

func newTUIModel(rt *RuntimeBundle, ctx context.Context) tuiModel {
	ti := textinput.New()
	ti.Placeholder = "Ask anything… (/ for commands)"
	ti.Focus()
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	vp := viewport.New(80, 20)
	return tuiModel{
		rt:         rt,
		ctx:        ctx,
		input:      ti,
		spin:       sp,
		viewport:   vp,
		autoFollow: true,
		models:     services.ListKnownModels(),
	}
}

func (m *tuiModel) appendLine(s string) { m.lines = append(m.lines, s) }

// flushCurrent settles the streaming buffer into the transcript.
func (m *tuiModel) flushCurrent() {
	if m.cur.Len() == 0 {
		return
	}
	if m.curIsReasoning {
		m.appendLine(tuiReasonStyle.Render(m.cur.String()))
	} else {
		m.appendLine(tuiTextBlock.Render(m.cur.String()))
	}
	m.cur.Reset()
	m.curIsReasoning = false
}

// refreshTranscript re-renders the viewport content, auto-scrolling only
// while the user is pinned to the bottom.
func (m *tuiModel) refreshTranscript() {
	m.viewport.SetContent(m.viewTranscript())
	if m.autoFollow {
		m.viewport.GotoBottom()
	}
}

func (m *tuiModel) Init() tea.Cmd { return textinput.Blink }

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = max(3, msg.Height-5)
		m.refreshTranscript()
		return m, nil

	case spinner.TickMsg:
		if !m.busy {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case statusMsg:
		m.status = msg.text
		return m, nil

	case streamEventMsg:
		m.applyStreamEvent(msg.ev)
		m.refreshTranscript()
		return m, nil

	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.autoFollow = false
			m.viewport.ScrollUp(3)
		case tea.MouseButtonWheelDown:
			m.viewport.ScrollDown(3)
			if m.viewport.AtBottom() {
				m.autoFollow = true
			}
		}
		return m, nil

	case queryDoneMsg:
		m.busy = false
		m.awaitingFirst = false
		m.flushCurrent()
		if msg.err != nil {
			m.appendLine(tuiErrStyle.Render("Error: " + msg.err.Error()))
		} else if m.lastBlockEmpty() {
			m.appendLine(tuiWarnStyle.Render("⚠ Model returned no visible content."))
		}
		m.refreshTranscript()
		return m, nil

	case compactDoneMsg:
		m.busy = false
		if msg.err != nil {
			m.appendLine(tuiErrStyle.Render("✖ Compact failed: " + msg.err.Error()))
		} else {
			m.appendLine(tuiOkStyle.Render(fmt.Sprintf("✔ Compacted: %d → %d tokens", msg.before, msg.after)))
		}
		m.refreshTranscript()
		return m, nil

	case hitlPromptMsg:
		m.mode = modeHitl
		m.hitl = &msg
		return m, nil
	}

	switch m.mode {
	case modeModelPicker:
		return m.updatePicker(msg)
	default:
		return m.updateInput(msg)
	}
}

func (m *tuiModel) lastBlockEmpty() bool {
	for i := len(m.lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(stripANSI(m.lines[i]))
		if l == "" || strings.HasPrefix(l, "[🧠") || strings.HasPrefix(l, "▶") ||
			strings.HasPrefix(l, "✔") || strings.HasPrefix(l, "✖") {
			continue
		}
		return false
	}
	return true
}

func (m *tuiModel) applyStreamEvent(ev engine.StreamEventWithUsage) {
	e := ev.Event
	switch e.Type {
	case engine.EventAborted:
		m.flushCurrent()
		m.finalizeAbortedTools()
		m.appendLine(tuiWarnStyle.Render("⏹ Aborted (session preserved)"))
	case engine.EventTextDelta:
		m.awaitingFirst = false
		m.cur.WriteString(e.Text)
	case engine.EventReasoningDelta:
		m.awaitingFirst = false
		m.curIsReasoning = true
		m.cur.WriteString(e.Text)
	case engine.EventToolExecutionStarted:
		m.awaitingFirst = false
		m.flushCurrent()
		line := tuiToolStyle.Render("◇ ") + tuiToolNameStyle.Render(e.ToolName) +
			tuiDimStyle.Render(" "+summarizeToolArgs(e.ToolInput))
		m.appendLine(line)
		m.openTools = append(m.openTools, len(m.lines)-1)
	case engine.EventToolExecutionCompleted:
		m.finishToolLine(e)
	}
}

// finishToolLine flips the matching running-tool line in place to its
// final ✔/✖ form instead of appending a new row.
func (m *tuiModel) finishToolLine(e engine.StreamEvent) {
	m.flushCurrent()
	idx := -1
	for i := len(m.openTools) - 1; i >= 0; i-- {
		if strings.Contains(stripANSI(m.lines[m.openTools[i]]), e.ToolName) {
			idx = i
			break
		}
	}
	var line string
	if e.ToolResult != nil && e.ToolResult.IsError {
		line = tuiErrStyle.Render(fmt.Sprintf("✖ %s failed", e.ToolName))
	} else {
		line = tuiOkStyle.Render(fmt.Sprintf("✔ %s", e.ToolName))
	}
	if idx >= 0 {
		m.lines[m.openTools[idx]] = line
		m.openTools = append(m.openTools[:idx], m.openTools[idx+1:]...)
		return
	}
	m.appendLine(line) // completion without a tracked start; keep it visible
}

// finalizeAbortedTools marks still-running tool lines as interrupted.
func (m *tuiModel) finalizeAbortedTools() {
	for _, li := range m.openTools {
		name := strings.TrimSpace(strings.TrimPrefix(stripANSI(m.lines[li]), "◇"))
		if i := strings.IndexAny(name, " \t"); i > 0 {
			name = name[:i]
		}
		m.lines[li] = tuiWarnStyle.Render(fmt.Sprintf("⏹ %s interrupted", name))
	}
	m.openTools = nil
}

// summarizeToolArgs renders tool input as sorted key=value pairs so the
// transcript line stays short and deterministic.
func summarizeToolArgs(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == "{}" {
		return ""
	}
	var kv map[string]any
	if err := json.Unmarshal(raw, &kv); err != nil || len(kv) == 0 {
		return truncateArgs(s)
	}
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := kv[k]
		if str, ok := v.(string); ok {
			parts = append(parts, fmt.Sprintf("%s=%s", k, str))
			continue
		}
		b, _ := json.Marshal(v)
		parts = append(parts, fmt.Sprintf("%s=%s", k, b))
	}
	return truncateArgs(strings.Join(parts, " "))
}

func truncateArgs(s string) string {
	s = strings.TrimSpace(s)
	const argCap = 72
	r := []rune(s)
	if len(r) > argCap {
		return string(r[:argCap]) + "…"
	}
	return s
}

func (m *tuiModel) updateInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	matches := m.slashMatches()
	menuOpen := len(matches) > 0
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.Type {
		case tea.KeyCtrlC:
			if m.busy {
				m.rt.Engine.Cancel()
				return m, nil
			}
			return m, tea.Quit
		case tea.KeyCtrlD:
			if !m.busy && m.mode != modeHitl {
				return m, tea.Quit
			}
			return m, nil
		case tea.KeyEsc:
			if menuOpen {
				m.input.SetValue("")
				m.syncSuggestions()
				return m, nil
			}
		case tea.KeyEnter:
			line := strings.TrimSpace(m.input.Value())
			picked := m.slashIdx
			m.input.SetValue("")
			m.input.SetSuggestions(nil)
			m.slashIdx = 0
			if menuOpen && matches[picked].cmd != line {
				// Enter on a highlighted menu row runs that command.
				return m, m.dispatch(matches[picked].cmd)
			}
			if m.mode == modeHitl {
				return m, m.answerHitl(line)
			}
			return m, m.dispatch(line)
		case tea.KeyTab:
			if menuOpen {
				m.input.SetValue(matches[m.slashIdx].cmd)
				m.syncSuggestions()
				return m, nil
			}
		case tea.KeyPgUp:
			m.autoFollow = false
			m.viewport.HalfPageUp()
			return m, nil
		case tea.KeyPgDown:
			m.viewport.HalfPageDown()
			if m.viewport.AtBottom() {
				m.autoFollow = true
			}
			return m, nil
		case tea.KeyUp:
			if menuOpen {
				if m.slashIdx > 0 {
					m.slashIdx--
				}
				return m, nil
			}
			m.autoFollow = false
			m.viewport.LineUp(1)
			return m, nil
		case tea.KeyDown:
			if menuOpen {
				if m.slashIdx < len(matches)-1 {
					m.slashIdx++
				}
				return m, nil
			}
			m.viewport.LineDown(1)
			if m.viewport.AtBottom() {
				m.autoFollow = true
			}
			return m, nil
		case tea.KeyHome:
			m.autoFollow = false
			m.viewport.GotoTop()
			return m, nil
		case tea.KeyEnd:
			m.autoFollow = true
			m.viewport.GotoBottom()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncSuggestions()
	return m, cmd
}

func (m *tuiModel) syncSuggestions() {
	v := m.input.Value()
	m.slashIdx = 0
	if !strings.HasPrefix(v, "/") || strings.Contains(v, " ") {
		m.input.SetSuggestions(nil)
		return
	}
	var sugg []string
	for _, c := range slashCommands {
		if strings.HasPrefix(c.cmd, v) && c.cmd != v {
			sugg = append(sugg, c.cmd)
		}
	}
	m.input.SetSuggestions(sugg)
}

// slashMatches lists the commands matching the current "/" input; empty
// means the menu is closed.
func (m *tuiModel) slashMatches() []slashCmd {
	v := m.input.Value()
	if m.mode != modeInput || !strings.HasPrefix(v, "/") || strings.Contains(v, " ") {
		return nil
	}
	var out []slashCmd
	for _, c := range slashCommands {
		if strings.HasPrefix(c.cmd, v) {
			out = append(out, c)
		}
	}
	return out
}

func (m tuiModel) viewSlashMenu() string {
	matches := m.slashMatches()
	if len(matches) == 0 {
		return ""
	}
	if m.slashIdx >= len(matches) {
		m.slashIdx = len(matches) - 1
	}
	var b strings.Builder
	for i, c := range matches {
		style := lipgloss.NewStyle()
		marker := "  "
		if i == m.slashIdx {
			style = style.Bold(true).Reverse(true)
			marker = "› "
		}
		b.WriteString(style.Render(fmt.Sprintf("%s%-9s %s", marker, c.cmd, c.desc)) + "\n")
	}
	b.WriteString(tuiDimStyle.Render("↑/↓ select · tab complete · enter run · esc dismiss") + "\n")
	return b.String()
}

// dispatch routes one submitted line: slash command or agent query.
func (m *tuiModel) dispatch(line string) tea.Cmd {
	if line == "" {
		return nil
	}
	switch {
	case line == "/exit" || line == "/quit":
		return tea.Quit
	case line == "/clear":
		m.rt.Engine.Clear()
		m.lines = nil
		m.status = "Conversation cleared."
		return nil
	case line == "/compact":
		if m.busy {
			m.status = "busy — wait for the current turn"
			return nil
		}
		m.busy = true
		m.appendLine(tuiDimStyle.Render(fmt.Sprintf("⏳ Compacting (%d tokens)…", m.rt.Engine.CurrentTokens())))
		m.refreshTranscript()
		rt, ctx := m.rt, m.ctx
		return func() tea.Msg {
			before, after, err := rt.Engine.CompactNow(ctx)
			return compactDoneMsg{before: before, after: after, err: err}
		}
	case line == "/cost":
		tok := m.rt.Engine.CurrentTokens()
		th := m.rt.Engine.CompactionThreshold()
		m.appendLine(tuiDimStyle.Render(fmt.Sprintf("Memory tokens: %d / %d", tok, th)))
		m.refreshTranscript()
		return nil
	case line == "/help":
		for _, c := range slashCommands {
			m.appendLine(tuiDimStyle.Render(fmt.Sprintf("  %-8s %s", c.cmd, c.desc)))
		}
		m.refreshTranscript()
		return nil
	case line == "/model" || line == "/models":
		m.mode = modeModelPicker
		m.cursor = 0
		return nil
	case strings.HasPrefix(line, "/model "):
		return m.doSwitchModel(strings.TrimSpace(strings.TrimPrefix(line, "/model")))
	default:
		m.appendLine(tuiUserStyle.Render("› " + line))
		if m.busy {
			// Single-flight: new input queues as steering, never dropped.
			m.rt.Engine.Submit(engine.SubmissionSteering, line)
			m.status = "(queued — delivered after current turn)"
			m.refreshTranscript()
			return nil
		}
		m.busy = true
		m.awaitingFirst = true
		m.refreshTranscript()
		return m.startQuery(line)
	}
}

func (m *tuiModel) doSwitchModel(id string) tea.Cmd {
	rt := m.rt
	return func() tea.Msg {
		if err := rt.SwitchModel(id, false); err != nil {
			return statusMsg{text: "✖ Switch failed: " + err.Error()}
		}
		return statusMsg{text: fmt.Sprintf("Switched to %s (window %d, threshold %d)",
			id, services.ContextWindowForModel(id), rt.Engine.CompactionThreshold())}
	}
}

// startQuery runs the agent loop in the background and streams every
// engine event back into the UI loop via program.Send.
func (m *tuiModel) startQuery(line string) tea.Cmd {
	p, ctx, rt := m.p, m.ctx, m.rt
	if p == nil {
		return func() tea.Msg { return statusMsg{text: "✖ internal: TUI program not bound"} }
	}
	return func() tea.Msg {
		ch := rt.Engine.SubmitMessage(ctx, line)
		go func() {
			var lastErr error
			for ev := range ch {
				if ev.Event.Error != nil {
					lastErr = ev.Event.Error
				}
				p.Send(streamEventMsg{ev: ev})
			}
			p.Send(queryDoneMsg{err: lastErr})
		}()
		return nil
	}
}

func (m *tuiModel) updatePicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c":
		if m.busy {
			m.rt.Engine.Cancel()
			return m, nil
		}
		return m, tea.Quit
	case "esc":
		m.mode = modeInput
	case "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down":
		if m.cursor < len(m.models)-1 {
			m.cursor++
		}
	case "enter":
		id := m.models[m.cursor].ID
		m.mode = modeInput
		return m, m.doSwitchModel(id)
	}
	return m, nil
}

// answerHitl resolves a pending HITL overlay with the typed answer.
func (m *tuiModel) answerHitl(answer string) tea.Cmd {
	h := m.hitl
	m.hitl = nil
	m.mode = modeInput
	if h == nil {
		return nil
	}
	ans := answer
	if n := len(h.options); n > 0 {
		if idx, ok := parseIndex(answer, n); ok {
			ans = h.options[idx]
		} else if answer == "" {
			ans = ""
		}
	}
	ch := h.reply
	return func() tea.Msg {
		ch <- ans
		return nil
	}
}

func (m *tuiModel) View() string {
	var b strings.Builder
	b.WriteString(m.viewHeader())
	b.WriteString("\n")
	b.WriteString(m.viewport.View())
	b.WriteString("\n")
	// TTFT: nothing streamed yet — show an animated thinking row.
	if m.busy && m.awaitingFirst && m.cur.Len() == 0 {
		b.WriteString(tuiTitleStyle.Render(m.spin.View() + " Thinking…"))
		b.WriteString("\n")
	}
	switch {
	case m.mode == modeModelPicker:
		b.WriteString(m.viewPicker())
	case m.mode == modeHitl && m.hitl != nil:
		b.WriteString(m.viewHitl())
	default:
		if menu := m.viewSlashMenu(); menu != "" {
			b.WriteString(menu)
		}
		b.WriteString(m.input.View())
		b.WriteString("\n")
		b.WriteString(m.viewFooter())
	}
	return b.String()
}

func (m tuiModel) viewHeader() string {
	st := m.rt.AppState.Get()
	tok := m.rt.Engine.CurrentTokens()
	th := m.rt.Engine.CompactionThreshold()
	color := "42"
	switch pct := pct(tok, th); {
	case pct > 80:
		color = "203"
	case pct > 50:
		color = "220"
	}
	left := tuiTitleStyle.Render("openharness") +
		tuiDimStyle.Render(fmt.Sprintf("  %s │ %s │ %s", st.Model, st.Provider, st.AuthStatus))
	right := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).
		Render(fmt.Sprintf("🧠 %.0f%%", pct(tok, th))) +
		tuiDimStyle.Render(fmt.Sprintf(" %d/%d", tok, th))
	if m.busy {
		right = m.spin.View() + right
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m tuiModel) viewTranscript() string {
	var b strings.Builder
	for _, l := range m.lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	if m.cur.Len() > 0 {
		if m.curIsReasoning {
			b.WriteString(tuiReasonStyle.Render(m.cur.String()))
		} else {
			b.WriteString(tuiTextBlock.Render(m.cur.String()))
		}
	}
	return b.String()
}

func (m tuiModel) viewPicker() string {
	var b strings.Builder
	b.WriteString(tuiTitleStyle.Render("Select model") +
		tuiDimStyle.Render("  ↑/↓ move · enter switch · esc cancel") + "\n")
	for i, mod := range m.models {
		marker := "  "
		style := lipgloss.NewStyle()
		if mod.ID == m.rt.Settings.Model {
			marker = "● "
			style = tuiOkStyle
		}
		if i == m.cursor {
			style = style.Bold(true).Reverse(true)
		}
		b.WriteString(style.Render(fmt.Sprintf("%s%-34s %9d", marker, mod.ID, mod.Window)) + "\n")
	}
	return b.String()
}

func (m tuiModel) viewHitl() string {
	var b strings.Builder
	b.WriteString(tuiWarnStyle.Render("? " + m.hitl.question) + "\n")
	for i, opt := range m.hitl.options {
		b.WriteString(fmt.Sprintf("  [%d] %s\n", i+1, opt))
	}
	return b.String()
}

func (m tuiModel) viewFooter() string {
	foot := tuiDimStyle.Render("enter send · tab autocomplete · ↑↓/pgup/wheel scroll · ctrl+c abort/quit")
	if m.status != "" {
		foot = tuiOkStyle.Render(m.status) + "  " + foot
	}
	return foot
}

func pct(a, b int) float64 {
	if b <= 0 {
		return 0
	}
	return float64(a) / float64(b) * 100
}

func parseIndex(s string, n int) (int, bool) {
	if s == "" || len(s) > 2 {
		return 0, false
	}
	idx := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		idx = idx*10 + int(r-'0')
	}
	if idx < 1 || idx > n {
		return 0, false
	}
	return idx - 1, true
}

// stripANSI removes escape sequences so empty-block detection sees text only.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !(s[j] >= 0x40 && s[j] <= 0x7e) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
