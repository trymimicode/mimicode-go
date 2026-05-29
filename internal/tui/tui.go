package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/trymimicode/mimicode-go/internal/agent"
	"github.com/trymimicode/mimicode-go/internal/compactor"
	"github.com/trymimicode/mimicode-go/internal/provider"
	"github.com/trymimicode/mimicode-go/internal/reflect"
	"github.com/trymimicode/mimicode-go/internal/store"
)

type streamMsg struct {
	Event string
	Data  map[string]any
}

type turnDoneMsg struct {
	Messages []provider.Message
	Err      error
	Usage    provider.Usage
}

type tickMsg time.Time

type line struct {
	Kind string
	Text string
}

type model struct {
	session  *store.Session
	cwd      string
	messages []provider.Message
	lines    []line
	input    string
	scroll   int
	width    int
	height   int

	running     bool
	cancel      context.CancelFunc
	step        int
	spinner     int
	lastTool    string
	modelName   string
	streamText  string
	currentCost float64
	enterSubmit bool

	program *tea.Program
}

var (
	userStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
	toolStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	statusStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("236"))
	inputStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("229"))
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

func RunTUI(sessionID string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	sess, messages, err := store.ResumeOrNew(sessionID, cwd, provider.DefaultModel())
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = reflect.RunReflect(rctx, sess, cwd)
	}()

	m := &model{
		session:  sess,
		cwd:      cwd,
		messages: messages,
		lines:    renderMessages(messages),
	}
	m.enterSubmit = os.Getenv("MIMICODE_TUI_ENTER_SUBMITS") != "0"
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.program = p
	_, err = p.Run()
	return err
}

func (m *model) Init() tea.Cmd {
	return tick()
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampScroll()
	case tea.KeyMsg:
		return m.handleKey(msg)
	case streamMsg:
		m.handleStream(msg)
	case turnDoneMsg:
		m.running = false
		m.cancel = nil
		m.messages = msg.Messages
		m.currentCost = estimateCost(msg.Usage)
		m.lines = renderMessages(m.messages)
		if msg.Err != nil {
			m.lines = append(m.lines, line{Kind: "error", Text: "error: " + msg.Err.Error()})
		}
		_ = m.session.SaveMessages(m.messages)
		next, record, err := compactor.MaybeCompact(context.Background(), m.messages, m.session.Path(), msg.Usage.InputTokens)
		if err == nil && record != nil {
			m.messages = next
			_ = m.session.SaveMessages(m.messages)
			m.lines = renderMessages(m.messages)
		}
		m.scrollToBottom()
	case tickMsg:
		if m.running {
			m.spinner++
			return m, tick()
		}
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlD:
		return m, tea.Quit
	case tea.KeyCtrlC:
		if m.running && m.cancel != nil {
			m.cancel()
			m.lines = append(m.lines, line{Kind: "tool", Text: "cancel requested"})
			m.scrollToBottom()
			return m, nil
		}
		return m, tea.Quit
	case tea.KeyEnter:
		if !m.running && m.enterSubmit {
			m.submit()
		}
	case tea.KeyCtrlJ:
		if !m.running {
			m.submit()
		}
	case tea.KeyBackspace, tea.KeyDelete:
		if !m.running && len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case tea.KeyUp:
		if m.scroll > 0 {
			m.scroll--
		}
	case tea.KeyDown:
		m.scroll++
		m.clampScroll()
	case tea.KeyPgUp:
		m.scroll -= viewHeight(m.height)
		m.clampScroll()
	case tea.KeyPgDown:
		m.scroll += viewHeight(m.height)
		m.clampScroll()
	default:
		if !m.running {
			m.input += msg.String()
		}
	}
	return m, nil
}

func (m *model) View() string {
	var b strings.Builder
	rows := viewHeight(m.height)
	rendered := m.renderedLines()
	m.clampScroll()

	end := m.scroll + rows
	if end > len(rendered) {
		end = len(rendered)
	}
	for i := m.scroll; i < end; i++ {
		b.WriteString(rendered[i])
		b.WriteString("\n")
	}
	for i := end - m.scroll; i < rows; i++ {
		b.WriteString("\n")
	}

	status := fmt.Sprintf(" session=%s model=%s cost=$%.4f", m.session.ID, shortModel(m.modelName), m.currentCost)
	if m.running {
		status += fmt.Sprintf(" step=%d %s %s", m.step, spinner(m.spinner), m.lastTool)
	}
	b.WriteString(statusStyle.Width(max(1, m.width)).Render(status))
	b.WriteString("\n")
	prompt := "> "
	if m.running {
		prompt = "… "
	}
	b.WriteString(inputStyle.Render(prompt + m.input))
	return b.String()
}

func (m *model) submit() {
	prompt := strings.TrimSpace(m.input)
	if prompt == "" {
		return
	}
	m.input = ""
	m.running = true
	m.step++
	m.modelName = provider.DefaultModel()
	m.streamText = ""
	m.lastTool = "thinking"
	m.messages = append(m.messages, provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: prompt,
		}},
	})
	m.lines = renderMessages(m.messages)
	m.scrollToBottom()

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	before := append([]provider.Message(nil), m.messages[:len(m.messages)-1]...)
	cb := func(eventType string, data map[string]any) {
		if m.program != nil {
			m.program.Send(streamMsg{Event: eventType, Data: data})
		}
	}
	go func() {
		next, err := agent.AgentTurn(ctx, agent.AgentConfig{
			CWD:      m.cwd,
			Session:  m.session,
			MaxSteps: 25,
			StreamCB: cb,
		}, prompt, before)
		if m.program != nil {
			m.program.Send(turnDoneMsg{Messages: next, Err: err, Usage: provider.LastUsage()})
		}
	}()
}

func (m *model) handleStream(msg streamMsg) {
	switch msg.Event {
	case provider.TextStart:
		m.streamText = ""
	case provider.TextDelta:
		text, _ := msg.Data["text"].(string)
		m.streamText += text
		m.replaceStreamingAssistant()
	case provider.ToolStart:
		name, _ := msg.Data["name"].(string)
		m.lastTool = name
		m.lines = append(m.lines, line{Kind: "tool", Text: "[tool] " + name + " started"})
	case provider.ToolComplete:
		name, _ := msg.Data["name"].(string)
		m.lastTool = name
		m.lines = append(m.lines, line{Kind: "tool", Text: "[tool] " + name + " complete"})
	}
	m.scrollToBottom()
}

func (m *model) replaceStreamingAssistant() {
	if m.streamText == "" {
		return
	}
	if len(m.lines) > 0 && m.lines[len(m.lines)-1].Kind == "assistant_stream" {
		m.lines[len(m.lines)-1].Text = m.streamText
		return
	}
	m.lines = append(m.lines, line{Kind: "assistant_stream", Text: m.streamText})
}

func (m *model) renderedLines() []string {
	var out []string
	for _, l := range m.lines {
		switch l.Kind {
		case "assistant", "assistant_stream":
			rendered := renderMarkdown(l.Text, m.width)
			rendered = strings.TrimLeft(rendered, "\n")
			lines := strings.Split(rendered, "\n")
			dotPlaced := false
			for _, physical := range lines {
				if !dotPlaced && strings.TrimSpace(physical) != "" {
					out = append(out, assistantStyle.Render("● ")+physical)
					dotPlaced = true
				} else {
					out = append(out, physical)
				}
			}
		default:
			for _, physical := range strings.Split(l.Text, "\n") {
				switch l.Kind {
				case "user":
					wrapped := wrapText("you: "+physical, m.width-2)
					for _, wl := range strings.Split(wrapped, "\n") {
						out = append(out, userStyle.Render(wl))
					}
				case "tool":
					out = append(out, toolStyle.Render(physical))
				case "error":
					out = append(out, errorStyle.Render(physical))
				default:
					out = append(out, physical)
				}
			}
		}
	}
	return out
}

func renderMarkdown(text string, width int) string {
	w := width - 4
	if w < 20 {
		w = 80
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(w),
	)
	if err != nil {
		return assistantStyle.Render(text)
	}
	out, err := r.Render(text)
	if err != nil {
		return assistantStyle.Render(text)
	}
	return strings.TrimRight(out, "\n")
}

func wrapText(text string, width int) string {
	if width <= 0 || len(text) <= width {
		return text
	}
	var result strings.Builder
	words := strings.Fields(text)
	lineLen := 0
	for i, word := range words {
		if i > 0 {
			if lineLen+1+len(word) > width {
				result.WriteByte('\n')
				lineLen = 0
			} else {
				result.WriteByte(' ')
				lineLen++
			}
		}
		result.WriteString(word)
		lineLen += len(word)
	}
	return result.String()
}

func (m *model) clampScroll() {
	rendered := m.renderedLines()
	maxScroll := len(rendered) - viewHeight(m.height)
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m *model) scrollToBottom() {
	rendered := m.renderedLines()
	m.scroll = len(rendered) - viewHeight(m.height)
	m.clampScroll()
}

func renderMessages(messages []provider.Message) []line {
	var lines []line
	for _, msg := range messages {
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				lines = append(lines, line{Kind: msg.Role, Text: block.Text})
			case "tool_use":
				lines = append(lines, line{Kind: "tool", Text: fmt.Sprintf("[tool:%s] %s", block.Name, formatInput(block.Input))})
			case "tool_result":
				lines = append(lines, line{Kind: "tool", Text: "[tool result] " + truncateLines(block.Content, 3)})
			}
		}
	}
	return lines
}

func formatInput(input map[string]any) string {
	if path, ok := input["path"].(string); ok && path != "" {
		return path
	}
	return fmt.Sprintf("%v", input)
}

func truncateLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:n], "\n") + "\n... (full output in session log)"
}

func tick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func spinner(i int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[i%len(frames)]
}

func shortModel(model string) string {
	switch model {
	case provider.ModelHaiku:
		return "haiku"
	case provider.ModelSonnet:
		return "sonnet"
	case "":
		return "-"
	default:
		return model
	}
}

func estimateCost(u provider.Usage) float64 {
	// Rough blended display-only estimate until model-specific accounting lands.
	return float64(u.InputTokens+u.OutputTokens) / 1_000_000 * 3.0
}

func viewHeight(height int) int {
	if height <= 3 {
		return 10
	}
	return height - 2
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
