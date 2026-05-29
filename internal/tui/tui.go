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
	"github.com/trymimicode/mimicode-go/internal/tools"
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
	Diff *tools.DiffInfo
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
	cursor   int

	running     bool
	cancel      context.CancelFunc
	step        int
	spinner     int
	lastTool    string
	toolStatus  string
	modelName   string
	streamText  string
	currentCost float64
	enterSubmit bool
	showOnboarding bool
	history     []string
	historyIdx  int

	program *tea.Program
}

var (
	userStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
	toolStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	statusStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("236"))
	inputStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("229"))
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	toolBarStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Background(lipgloss.Color("237"))
	diffAddStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("34")).Background(lipgloss.Color("22"))
	diffRemStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Background(lipgloss.Color("52"))
	diffLineStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	diffFileStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
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
		cursor:   0,
		history:  []string{},
		historyIdx: -1,
	}
	m.enterSubmit = os.Getenv("MIMICODE_TUI_ENTER_SUBMITS") == "1"
	// Show onboarding if this is a new session with no messages
	m.showOnboarding = len(messages) == 0
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
		m.toolStatus = ""
		m.spinner = 0  // Reset spinner to fix stuck loader
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
			m.replaceStreamingAssistant()
			m.scrollToBottom()
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
		if !m.running {
			if m.showOnboarding {
				m.showOnboarding = false
			} else if msg.Alt {
				// Alt+Enter = new line (Shift+Enter is not distinguishable in terminals)
				left := m.input[:m.cursor]
				right := m.input[m.cursor:]
				m.input = left + "\n" + right
				m.cursor++
			} else {
				m.submit()
				return m, tick()
			}
		}
	case tea.KeyBackspace, tea.KeyDelete:
		if !m.running && len(m.input) > 0 && m.cursor > 0 {
			left := m.input[:m.cursor-1]
			right := m.input[m.cursor:]
			m.input = left + right
			m.cursor--
		}
	case tea.KeyLeft:
		if !m.running && m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyRight:
		if !m.running && m.cursor < len(m.input) {
			m.cursor++
		}
	case tea.KeyHome:
		if !m.running {
			lineStart := strings.LastIndex(m.input[:m.cursor], "\n")
			if lineStart == -1 {
				m.cursor = 0
			} else {
				m.cursor = lineStart + 1
			}
		}
	case tea.KeyEnd:
		if !m.running {
			rest := m.input[m.cursor:]
			lineEnd := strings.Index(rest, "\n")
			if lineEnd == -1 {
				m.cursor = len(m.input)
			} else {
				m.cursor += lineEnd
			}
		}
	case tea.KeyUp:
		if !m.running {
			if strings.Contains(m.input, "\n") {
				// Move cursor up in multiline input
				lines := strings.Split(m.input, "\n")
				pos := 0
				lineIdx := 0
				for i, line := range lines {
					if pos+len(line) >= m.cursor {
						lineIdx = i
						break
					}
					pos += len(line) + 1
				}
				if lineIdx > 0 {
					posInLine := m.cursor - pos
					prevLineLen := len(lines[lineIdx-1])
					if posInLine > prevLineLen {
						posInLine = prevLineLen
					}
					m.cursor = pos - len(lines[lineIdx-1]) - 1 + posInLine
				}
			} else if m.input == "" && len(m.history) > 0 {
				// Navigate history when input is empty
				if m.historyIdx == -1 {
					m.historyIdx = len(m.history) - 1
				} else if m.historyIdx > 0 {
					m.historyIdx--
				}
				if m.historyIdx >= 0 && m.historyIdx < len(m.history) {
					m.input = m.history[m.historyIdx]
					m.cursor = len(m.input)
				}
			} else if m.scroll > 0 {
				m.scroll--
			}
		} else if m.scroll > 0 {
			m.scroll--
		}
	case tea.KeyDown:
		if !m.running {
			if strings.Contains(m.input, "\n") {
				// Move cursor down in multiline input
				lines := strings.Split(m.input, "\n")
				pos := 0
				lineIdx := 0
				for i, line := range lines {
					if pos+len(line) >= m.cursor {
						lineIdx = i
						break
					}
					pos += len(line) + 1
				}
				if lineIdx < len(lines)-1 {
					posInLine := m.cursor - pos
					nextLineLen := len(lines[lineIdx+1])
					if posInLine > nextLineLen {
						posInLine = nextLineLen
					}
					m.cursor = pos + len(lines[lineIdx]) + 1 + posInLine
				}
			} else if m.input == "" && m.historyIdx != -1 {
				// Navigate history forward
				if m.historyIdx < len(m.history)-1 {
					m.historyIdx++
					m.input = m.history[m.historyIdx]
					m.cursor = len(m.input)
				} else {
					m.historyIdx = -1
					m.input = ""
					m.cursor = 0
				}
			} else {
				m.scroll++
				m.clampScroll()
			}
		} else {
			m.scroll++
			m.clampScroll()
		}
	case tea.KeyPgUp:
		m.scroll -= viewHeight(m.height)
		m.clampScroll()
	case tea.KeyPgDown:
		m.scroll += viewHeight(m.height)
		m.clampScroll()
	case tea.KeyTab:
		// Tab for indentation in multiline mode
		if !m.running {
			left := m.input[:m.cursor]
			right := m.input[m.cursor:]
			m.input = left + "    " + right
			m.cursor += 4
		}
	case tea.KeyCtrlA:
		// Move to start of current line
		if !m.running {
			if strings.Contains(m.input, "\n") {
				lines := strings.Split(m.input, "\n")
				pos := 0
				for _, line := range lines {
					if pos+len(line) >= m.cursor {
						m.cursor = pos
						break
					}
					pos += len(line) + 1
				}
			} else {
				m.cursor = 0
			}
		}
	case tea.KeyCtrlE:
		// Move to end of current line
		if !m.running {
			if strings.Contains(m.input, "\n") {
				lines := strings.Split(m.input, "\n")
				pos := 0
				for _, line := range lines {
					if pos+len(line) >= m.cursor {
						m.cursor = pos + len(line)
						break
					}
					pos += len(line) + 1
				}
			} else {
				m.cursor = len(m.input)
			}
		}
	default:
		if !m.running {
			left := m.input[:m.cursor]
			right := m.input[m.cursor:]
			m.input = left + msg.String() + right
			m.cursor += len(msg.String())
		}
	}
	return m, nil
}

func (m *model) View() string {
	var b strings.Builder
	
	// Show onboarding screen if needed
	if m.showOnboarding {
		return m.renderOnboarding()
	}
	
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

	// Tool bar (if active)
	if m.running && m.toolStatus != "" {
		toolBar := fmt.Sprintf(" %s %s", spinner(m.spinner), m.toolStatus)
		b.WriteString(toolBarStyle.Width(max(1, m.width)).Render(toolBar))
		b.WriteString("\n")
	}

	// Status bar with input mode indicator
	status := fmt.Sprintf(" session=%s model=%s cost=$%.4f", m.session.ID, shortModel(m.modelName), m.currentCost)
	if m.running {
		status += fmt.Sprintf(" step=%d", m.step)
	} else {
		if m.enterSubmit {
			status += " [Enter=Send, Shift+Enter=Newline]"
		} else {
			status += " [Shift+Enter=Newline, Ctrl+J=Send]"
		}
	}
	b.WriteString(statusStyle.Width(max(1, m.width)).Render(status))
	b.WriteString("\n")
	
	// Input area with word wrapping
	prompt := "> "
	if m.running {
		prompt = "… "
	} else if strings.Contains(m.input, "\n") {
		prompt = "│ "
	}
	
	// Render multiline input with cursor
	inputLines := wrapInput(m.input, m.width-len(prompt)-2, m.cursor)
	for i, line := range inputLines {
		if i == 0 {
			b.WriteString(inputStyle.Render(prompt + line))
		} else {
			b.WriteString(inputStyle.Render(strings.Repeat(" ", len(prompt)) + line))
		}
		if i < len(inputLines)-1 {
			b.WriteString("\n")
		}
	}
	
	return b.String()
}

func (m *model) submit() {
	prompt := strings.TrimSpace(m.input)
	if prompt == "" {
		return
	}
	
	// Add to history
	m.history = append(m.history, m.input)
	if len(m.history) > 100 {
		m.history = m.history[1:]
	}
	m.historyIdx = -1
	
	m.input = ""
	m.cursor = 0
	m.running = true
	m.step++
	m.spinner = 0  // Reset spinner
	m.modelName = provider.DefaultModel()
	m.streamText = ""
	m.lastTool = "thinking"
	m.toolStatus = "thinking..."
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
		m.toolStatus = ""
	case provider.TextDelta:
		text, _ := msg.Data["text"].(string)
		m.streamText += text
		// Flushed to display on tick (120ms) to avoid per-token jitter.
	case provider.ToolStart:
		name, _ := msg.Data["name"].(string)
		m.lastTool = name
		m.toolStatus = fmt.Sprintf("Running %s", name)
		m.scrollToBottom()
	case provider.ToolComplete:
		name, _ := msg.Data["name"].(string)
		m.lastTool = name
		m.toolStatus = fmt.Sprintf("Completed %s", name)
		m.scrollToBottom()
	case "file_change":
		path, _ := msg.Data["path"].(string)
		oldContent, _ := msg.Data["old_content"].(string)
		newContent, _ := msg.Data["new_content"].(string)
		operation, _ := msg.Data["operation"].(string)
		isNewFile, _ := msg.Data["is_new_file"].(bool)
		m.lines = append(m.lines, line{
			Kind: "diff",
			Diff: &tools.DiffInfo{
				Path:       path,
				OldContent: oldContent,
				NewContent: newContent,
				Operation:  operation,
				IsNewFile:  isNewFile,
			},
		})
		m.scrollToBottom()
	}
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
		case "diff":
			if l.Diff != nil {
				out = append(out, renderDiff(l.Diff, m.width)...)
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

func renderDiff(diff *tools.DiffInfo, width int) []string {
	var out []string
	
	// File header
	header := fmt.Sprintf("━━━ %s %s ━━━", diff.Operation, diff.Path)
	out = append(out, diffFileStyle.Render(header))
	
	if diff.IsNewFile {
		out = append(out, diffAddStyle.Render("+ New file"))
		lines := strings.Split(diff.NewContent, "\n")
		for i, line := range lines {
			lineNum := fmt.Sprintf("%4d", i+1)
			out = append(out, diffLineStyle.Render(lineNum)+" "+diffAddStyle.Render("+ "+line))
		}
	} else {
		// Generate line-by-line diff
		oldLines := strings.Split(diff.OldContent, "\n")
		newLines := strings.Split(diff.NewContent, "\n")
		
		// Simple diff algorithm - show removed lines then added lines
		maxOld := len(oldLines)
		maxNew := len(newLines)
		
		i, j := 0, 0
		for i < maxOld || j < maxNew {
			if i < maxOld && j < maxNew && oldLines[i] == newLines[j] {
				// Unchanged line
				lineNum := fmt.Sprintf("%4d", i+1)
				out = append(out, diffLineStyle.Render(lineNum)+"  "+oldLines[i])
				i++
				j++
			} else if i < maxOld {
				// Removed line
				lineNum := fmt.Sprintf("%4d", i+1)
				out = append(out, diffLineStyle.Render(lineNum)+" "+diffRemStyle.Render("- "+oldLines[i]))
				i++
			} else if j < maxNew {
				// Added line
				lineNum := fmt.Sprintf("%4d", j+1)
				out = append(out, diffLineStyle.Render(lineNum)+" "+diffAddStyle.Render("+ "+newLines[j]))
				j++
			}
		}
	}
	
	out = append(out, "")
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

func wrapInput(text string, width int, cursor int) []string {
	if text == "" {
		return []string{"|"} // Show cursor on empty input
	}
	
	// Insert cursor marker
	var cursorText string
	if cursor >= len(text) {
		cursorText = text + "|"
	} else {
		cursorText = text[:cursor] + "|" + text[cursor:]
	}
	
	// Split by newlines first
	lines := strings.Split(cursorText, "\n")
	var wrapped []string
	
	for _, line := range lines {
		if width <= 0 || len(line) <= width {
			wrapped = append(wrapped, line)
		} else {
			// Wrap long lines at word boundaries when possible
			words := strings.Fields(line)
			if len(words) == 0 {
				// No words, just hard wrap
				for len(line) > width {
					wrapped = append(wrapped, line[:width])
					line = line[width:]
				}
				if line != "" {
					wrapped = append(wrapped, line)
				}
			} else {
				// Wrap at word boundaries
				current := ""
				for _, word := range words {
					if current == "" {
						current = word
					} else if len(current) + 1 + len(word) <= width {
						current += " " + word
					} else {
						wrapped = append(wrapped, current)
						current = word
					}
				}
				if current != "" {
					wrapped = append(wrapped, current)
				}
			}
		}
	}
	
	return wrapped
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
				// Skip tool_use blocks in chat display
			case "tool_result":
				// Check if this is a diff result
				if strings.Contains(block.ToolUseID, "diff") {
					lines = append(lines, line{Kind: "tool", Text: "[tool result] File modified"})
				}
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
	case provider.ModelOpus:
		return "opus"
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
	// Account for status bar, tool bar (if shown), and input line(s)
	return height - 3
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m *model) renderOnboarding() string {
	var b strings.Builder
	
	// Center the onboarding content
	topPadding := max(0, (m.height - 20) / 2)
	for i := 0; i < topPadding; i++ {
		b.WriteString("\n")
	}
	
	welcome := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214")).
		Bold(true).
		Align(lipgloss.Center).
		Width(m.width)
	
	normal := lipgloss.NewStyle().
		Foreground(lipgloss.Color("250")).
		Align(lipgloss.Center).
		Width(m.width)
		
	highlight := lipgloss.NewStyle().
		Foreground(lipgloss.Color("86")).
		Bold(true)
	
	b.WriteString(welcome.Render("Welcome to Mimicode"))
	b.WriteString("\n\n")
	b.WriteString(normal.Render("Your AI coding assistant for snippets & tasks"))
	b.WriteString("\n\n")
	b.WriteString(normal.Render("──────────────────────────"))
	b.WriteString("\n\n")
	
	// Key bindings
	keys := []string{
		"• " + highlight.Render("Shift+Enter") + " - Add new line",
		"• " + highlight.Render("Ctrl+J") + " or " + highlight.Render("Enter") + " - Send message",
		"• " + highlight.Render("↑/↓") + " - Navigate history",
		"• " + highlight.Render("Ctrl+C") + " - Cancel or quit",
		"• " + highlight.Render("Ctrl+D") + " - Exit",
	}
	
	for _, key := range keys {
		b.WriteString(lipgloss.NewStyle().PaddingLeft(m.width/2 - 20).Render(key))
		b.WriteString("\n")
	}
	
	b.WriteString("\n")
	b.WriteString(normal.Render("──────────────────────────"))
	b.WriteString("\n\n")
	
	// Example prompts
	b.WriteString(normal.Render("Try asking:"))
	b.WriteString("\n")
	examples := []string{
		`"Write a function to validate emails"`,
		`"Help me fix this error: ..."`,
		`"Convert this Python code to Go"`,
		`"What's the best way to handle errors in React?"`,
	}
	
	exampleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("229")).
		Italic(true)
	
	for _, ex := range examples {
		b.WriteString(lipgloss.NewStyle().PaddingLeft(m.width/2 - 20).Render("• " + exampleStyle.Render(ex)))
		b.WriteString("\n")
	}
	
	b.WriteString("\n\n")
	b.WriteString(normal.Render("Press Enter to continue..."))
	
	return b.String()
}