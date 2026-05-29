package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

const (
	modeChat = 0
	modeDiff = 1
)

type slashDef struct{ cmd, args, desc string }

var slashDefs = []slashDef{
	{"clear", "", "Clear the chat"},
	{"model", "[haiku|sonnet|opus]", "Switch AI model"},
	{"usage", "", "Show token usage"},
	{"help", "", "List commands"},
	{"new", "", "Start a new session"},
	{"diff", "", "Browse changed files"},
}

type line struct {
	Kind string
	Text string
	Diff *tools.DiffInfo
}

// readingFile tracks the animated "reading" state for a single file.
type readingFile struct {
	path    string
	lines   []string
	cursor  int
	speed   int
	lineIdx int // index in m.lines where the placeholder sits
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

	running        bool
	cancel         context.CancelFunc
	step           int
	spinner        int
	lastTool       string
	toolStatus     string
	modelName      string
	streamText     string
	currentCost    float64
	showOnboarding bool
	history        []string
	historyIdx     int

	reading      *readingFile // active reading animation, nil when idle
	allToolLines []line       // tool diffs/reads accumulated across all turns

	program    *tea.Program
	lineCache  []string // cached output of renderedLines()
	cacheDirty bool     // true when lineCache must be recomputed

	// files bar / diff view
	mode         int             // modeChat | modeDiff
	changedFiles []tools.DiffInfo // deduplicated, latest diff per path
	fileIdx      int             // selected file (files bar and diff view)
	fileBarFocus bool            // files bar has keyboard focus
	diffScroll   int             // scroll offset within diff view

	// slash commands
	slashSuggest []int // matching slashDefs indices for current input
	slashSelIdx  int   // which suggestion is highlighted

	// accumulated usage across all turns
	totalUsage provider.Usage

	// model override (set by /model command)
	modelOverride string
}

var (
	userStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	assistantStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
	toolStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	statusStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("236"))
	inputStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("229"))
	errorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	toolBarStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Background(lipgloss.Color("237"))
	diffFileStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	diffLineNumStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	diffAddMarkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("34")).Bold(true)
	diffRemMarkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	diffCodeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	diffFadedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	readCursorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Background(lipgloss.Color("238")).Bold(true)
	readDimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	streamHeadStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)

	// files bar
	fileTabStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Background(lipgloss.Color("234")).PaddingLeft(1).PaddingRight(1)
	fileTabActiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("238")).Bold(true).PaddingLeft(1).PaddingRight(1)
	fileBarBgStyle     = lipgloss.NewStyle().Background(lipgloss.Color("234"))

	// diff view
	diffTabBarStyle    = lipgloss.NewStyle().Background(lipgloss.Color("235"))
	diffTabItemStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Background(lipgloss.Color("235")).PaddingLeft(1).PaddingRight(1)
	diffTabActiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Background(lipgloss.Color("238")).Bold(true).PaddingLeft(1).PaddingRight(1)
	diffHintStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Background(lipgloss.Color("235"))

	// slash menu
	slashMenuBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Background(lipgloss.Color("234"))
	slashItemStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("234")).PaddingLeft(2)
	slashItemSelStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("237")).Bold(true).PaddingLeft(1)
	slashCmdStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
	slashArgStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
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
	// Show onboarding if this is a new session with no messages
	m.showOnboarding = len(messages) == 0
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
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
		m.bumpCache()
		m.clampScroll()
	case tea.KeyMsg:
		return m.handleKey(msg)
	case streamMsg:
		m.handleStream(msg)
	case turnDoneMsg:
		// If reading was still animating, finalize it now.
		if m.reading != nil {
			summary := line{Kind: "tool", Text: fmt.Sprintf("read %s (%d lines)", filepath.Base(m.reading.path), len(m.reading.lines))}
			if m.reading.lineIdx < len(m.lines) {
				m.lines[m.reading.lineIdx] = summary
			}
			m.allToolLines = append(m.allToolLines, summary)
			m.reading = nil
		}
		// Promote streaming assistant lines to permanent (triggers full glamour render).
		for i, l := range m.lines {
			if l.Kind == "assistant_stream" {
				m.lines[i].Kind = "assistant"
			}
		}
		m.running = false
		m.cancel = nil
		m.toolStatus = ""
		m.spinner = 0
		m.messages = msg.Messages
		m.totalUsage.InputTokens += msg.Usage.InputTokens
		m.totalUsage.OutputTokens += msg.Usage.OutputTokens
		m.currentCost = estimateCost(msg.Usage)
		if msg.Err != nil {
			m.lines = append(m.lines, line{Kind: "error", Text: "error: " + msg.Err.Error()})
		}
		if m.session != nil {
			_ = m.session.SaveMessages(m.messages)
			next, record, err := compactor.MaybeCompact(context.Background(), m.messages, m.session.Path(), msg.Usage.InputTokens)
			if err == nil && record != nil {
				m.messages = next
				_ = m.session.SaveMessages(m.messages)
				m.lines = append(renderMessages(m.messages), m.allToolLines...)
			}
		}
		m.bumpCache()
		m.scrollToBottom()
	case tickMsg:
		if m.running {
			m.spinner++
			m.replaceStreamingAssistant()
			if m.reading != nil {
				m.reading.cursor += m.reading.speed
				m.bumpCache() // cursor advanced, re-render reading window
				if m.reading.cursor >= len(m.reading.lines) {
					summary := line{Kind: "tool", Text: fmt.Sprintf("read %s (%d lines)", filepath.Base(m.reading.path), len(m.reading.lines))}
					if m.reading.lineIdx < len(m.lines) {
						m.lines[m.reading.lineIdx] = summary
					}
					m.allToolLines = append(m.allToolLines, summary)
					m.reading = nil
				}
			}
			m.scrollToBottom()
			return m, tick()
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.handleMouseClick(msg.X, msg.Y)
		}
	}
	return m, nil
}

func (m *model) handleMouseClick(x, y int) {
	// Files bar is at a fixed row from the bottom:
	// height - (1 input + 1 status + 1 files bar) = height - 3
	if len(m.changedFiles) == 0 || m.mode == modeDiff {
		return
	}
	filesBarRow := m.height - 3
	if m.running && m.toolStatus != "" {
		filesBarRow--
	}
	if y != filesBarRow {
		return
	}
	// Estimate which tab was clicked: each tab is " basename " = len+2 padding + separator
	offset := 1
	for i, f := range m.changedFiles {
		name := filepath.Base(f.Path)
		tabWidth := len(name) + 3 // padding + space before separator
		if x >= offset && x < offset+tabWidth {
			m.fileIdx = i
			m.fileBarFocus = true
			return
		}
		offset += tabWidth + 2 // +2 for separator " │"
	}
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ── Diff view mode ───────────────────────────────────────────────────────
	if m.mode == modeDiff {
		switch msg.Type {
		case tea.KeyCtrlD:
			return m, tea.Quit
		case tea.KeyCtrlC, tea.KeyEsc:
			m.mode = modeChat
		case tea.KeyLeft:
			if m.fileIdx > 0 {
				m.fileIdx--
				m.diffScroll = 0
			}
		case tea.KeyRight:
			if m.fileIdx < len(m.changedFiles)-1 {
				m.fileIdx++
				m.diffScroll = 0
			}
		case tea.KeyUp:
			if m.diffScroll > 0 {
				m.diffScroll--
			}
		case tea.KeyDown:
			m.diffScroll++
		case tea.KeyPgUp:
			m.diffScroll -= m.height - 2
			if m.diffScroll < 0 {
				m.diffScroll = 0
			}
		case tea.KeyPgDown:
			m.diffScroll += m.height - 2
		}
		return m, nil
	}

	// ── Files bar focused ────────────────────────────────────────────────────
	if m.fileBarFocus {
		switch msg.Type {
		case tea.KeyEsc, tea.KeyTab:
			m.fileBarFocus = false
		case tea.KeyLeft:
			if m.fileIdx > 0 {
				m.fileIdx--
			}
		case tea.KeyRight:
			if m.fileIdx < len(m.changedFiles)-1 {
				m.fileIdx++
			}
		case tea.KeyEnter:
			if len(m.changedFiles) > 0 {
				m.mode = modeDiff
				m.diffScroll = 0
			}
		case tea.KeyCtrlD:
			return m, tea.Quit
		case tea.KeyCtrlC:
			return m, tea.Quit
		}
		return m, nil
	}

	// ── Chat mode ────────────────────────────────────────────────────────────
	switch msg.Type {
	case tea.KeyCtrlD:
		return m, tea.Quit

	case tea.KeyCtrlC:
		if m.running && m.cancel != nil {
			m.cancel()
			m.lines = append(m.lines, line{Kind: "tool", Text: "cancel requested"})
			m.bumpCache()
			m.scrollToBottom()
			return m, nil
		}
		return m, tea.Quit

	case tea.KeyEsc:
		// Dismiss slash menu
		m.slashSuggest = nil
		m.slashSelIdx = 0

	case tea.KeyEnter:
		if !m.running {
			if m.showOnboarding {
				m.showOnboarding = false
			} else if len(m.slashSuggest) > 0 {
				// Execute highlighted slash command
				def := slashDefs[m.slashSuggest[m.slashSelIdx]]
				m.input = ""
				m.cursor = 0
				m.slashSuggest = nil
				m.slashSelIdx = 0
				m.executeSlash(def.cmd, nil)
				return m, nil
			} else if strings.HasPrefix(strings.TrimSpace(m.input), "/") {
				// Execute typed slash command directly
				parts := strings.Fields(m.input)
				cmd := strings.TrimPrefix(parts[0], "/")
				m.input = ""
				m.cursor = 0
				m.slashSuggest = nil
				m.slashSelIdx = 0
				m.executeSlash(cmd, parts[1:])
				return m, nil
			} else if msg.Alt {
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
			m.updateSlashSuggest()
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
		if len(m.slashSuggest) > 0 {
			m.slashSelIdx--
			if m.slashSelIdx < 0 {
				m.slashSelIdx = len(m.slashSuggest) - 1
			}
		} else if !m.running {
			if strings.Contains(m.input, "\n") {
				lines := strings.Split(m.input, "\n")
				pos, lineIdx := 0, 0
				for i, l := range lines {
					if pos+len(l) >= m.cursor {
						lineIdx = i
						break
					}
					pos += len(l) + 1
				}
				if lineIdx > 0 {
					posInLine := m.cursor - pos
					prevLen := len(lines[lineIdx-1])
					if posInLine > prevLen {
						posInLine = prevLen
					}
					m.cursor = pos - prevLen - 1 + posInLine
				}
			} else if m.input == "" && len(m.history) > 0 {
				if m.historyIdx == -1 {
					m.historyIdx = len(m.history) - 1
				} else if m.historyIdx > 0 {
					m.historyIdx--
				}
				if m.historyIdx >= 0 {
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
		if len(m.slashSuggest) > 0 {
			m.slashSelIdx = (m.slashSelIdx + 1) % len(m.slashSuggest)
		} else if !m.running {
			if strings.Contains(m.input, "\n") {
				lines := strings.Split(m.input, "\n")
				pos, lineIdx := 0, 0
				for i, l := range lines {
					if pos+len(l) >= m.cursor {
						lineIdx = i
						break
					}
					pos += len(l) + 1
				}
				if lineIdx < len(lines)-1 {
					posInLine := m.cursor - pos
					nextLen := len(lines[lineIdx+1])
					if posInLine > nextLen {
						posInLine = nextLen
					}
					m.cursor = pos + len(lines[lineIdx]) + 1 + posInLine
				}
			} else if m.input == "" && m.historyIdx != -1 {
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
		m.scroll -= m.chatRows()
		m.clampScroll()

	case tea.KeyPgDown:
		m.scroll += m.chatRows()
		m.clampScroll()

	case tea.KeyTab:
		if !m.running {
			if len(m.slashSuggest) > 0 {
				// Complete to selected command
				def := slashDefs[m.slashSuggest[m.slashSelIdx]]
				suffix := ""
				if def.args != "" {
					suffix = " "
				}
				m.input = "/" + def.cmd + suffix
				m.cursor = len(m.input)
				m.updateSlashSuggest()
			} else if m.input == "" && len(m.changedFiles) > 0 {
				// Focus files bar
				m.fileBarFocus = true
			} else {
				left := m.input[:m.cursor]
				right := m.input[m.cursor:]
				m.input = left + "    " + right
				m.cursor += 4
			}
		}

	case tea.KeyCtrlA:
		if !m.running {
			if strings.Contains(m.input, "\n") {
				lines := strings.Split(m.input, "\n")
				pos := 0
				for _, l := range lines {
					if pos+len(l) >= m.cursor {
						m.cursor = pos
						break
					}
					pos += len(l) + 1
				}
			} else {
				m.cursor = 0
			}
		}

	case tea.KeyCtrlE:
		if !m.running {
			if strings.Contains(m.input, "\n") {
				lines := strings.Split(m.input, "\n")
				pos := 0
				for _, l := range lines {
					if pos+len(l) >= m.cursor {
						m.cursor = pos + len(l)
						break
					}
					pos += len(l) + 1
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
			m.updateSlashSuggest()
		}
	}
	return m, nil
}

func (m *model) View() string {
	if m.showOnboarding {
		return m.renderOnboarding()
	}

	// ── Diff view mode ───────────────────────────────────────────────────────
	if m.mode == modeDiff {
		return m.renderDiffView()
	}

	// ── Chat mode ────────────────────────────────────────────────────────────
	var b strings.Builder
	rows := m.chatRows()
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

	// Slash command menu (above files bar / status)
	if len(m.slashSuggest) > 0 {
		b.WriteString(m.renderSlashMenu())
	}

	// Files bar
	if len(m.changedFiles) > 0 {
		b.WriteString(m.renderFilesBar())
		b.WriteString("\n")
	}

	// Tool bar
	if m.running && m.toolStatus != "" {
		toolBar := fmt.Sprintf(" %s %s", spinner(m.spinner), m.toolStatus)
		b.WriteString(toolBarStyle.Width(max(1, m.width)).Render(toolBar))
		b.WriteString("\n")
	}

	// Status bar
	modelDisplay := shortModel(m.modelName)
	if m.modelOverride != "" {
		modelDisplay = shortModel(m.modelOverride)
	}
	status := fmt.Sprintf(" session=%s model=%s cost=$%.4f", m.session.ID, modelDisplay, m.currentCost)
	if m.running {
		status += fmt.Sprintf(" step=%d", m.step)
	} else {
		hints := "[Enter=Send  Alt+Enter=Newline"
		if len(m.changedFiles) > 0 {
			hints += "  Tab=Files"
		}
		hints += "]"
		status += "  " + hints
	}
	b.WriteString(statusStyle.Width(max(1, m.width)).Render(status))
	b.WriteString("\n")

	// Input area
	prompt := "> "
	if m.running {
		prompt = "… "
	} else if strings.Contains(m.input, "\n") {
		prompt = "│ "
	}
	inputLines := wrapInput(m.input, m.width-len(prompt)-2, m.cursor)
	for i, ln := range inputLines {
		if i == 0 {
			b.WriteString(inputStyle.Render(prompt + ln))
		} else {
			b.WriteString(inputStyle.Render(strings.Repeat(" ", len(prompt)) + ln))
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
	m.slashSuggest = nil
	m.slashSelIdx = 0
	m.running = true
	m.step++
	m.spinner = 0
	if m.modelOverride != "" {
		m.modelName = m.modelOverride
	} else {
		m.modelName = provider.DefaultModel()
	}
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
	m.lines = append(m.lines, line{Kind: "user", Text: prompt})
	m.bumpCache()
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
			Model:    m.modelName,
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
		diff := tools.DiffInfo{
			Path:       path,
			OldContent: oldContent,
			NewContent: newContent,
			Operation:  operation,
			IsNewFile:  isNewFile,
		}
		l := line{Kind: "diff", Diff: &diff}
		m.lines = append(m.lines, l)
		m.allToolLines = append(m.allToolLines, l)
		m.upsertChangedFile(diff)
		m.bumpCache()
		m.scrollToBottom()
	case "file_read":
		path, _ := msg.Data["path"].(string)
		output, _ := msg.Data["output"].(string)
		lines := parseReadOutput(output)
		if len(lines) > 0 {
			speed := len(lines) / 40
			if speed < 2 {
				speed = 2
			}
			lineIdx := len(m.lines)
			m.lines = append(m.lines, line{Kind: "reading", Text: path})
			m.reading = &readingFile{
				path:    path,
				lines:   lines,
				cursor:  0,
				speed:   speed,
				lineIdx: lineIdx,
			}
			m.bumpCache()
			m.scrollToBottom()
		}
	}
}

func (m *model) replaceStreamingAssistant() {
	if m.streamText == "" {
		return
	}
	if len(m.lines) > 0 && m.lines[len(m.lines)-1].Kind == "assistant_stream" {
		m.lines[len(m.lines)-1].Text = m.streamText
		m.bumpCache()
		return
	}
	m.lines = append(m.lines, line{Kind: "assistant_stream", Text: m.streamText})
	m.bumpCache()
}

func (m *model) bumpCache() {
	m.cacheDirty = true
}

func (m *model) renderedLines() []string {
	if !m.cacheDirty && m.lineCache != nil {
		return m.lineCache
	}
	m.lineCache = m.computeRenderedLines()
	m.cacheDirty = false
	return m.lineCache
}

func (m *model) computeRenderedLines() []string {
	var out []string
	for i, l := range m.lines {
		switch l.Kind {
		case "reading":
			if m.reading != nil && m.reading.lineIdx == i {
				out = append(out, renderReadingWindow(m.reading, m.width)...)
			} else {
				out = append(out, toolStyle.Render(l.Text))
			}
		case "assistant":
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
		case "assistant_stream":
			// Don't run glamour on incomplete streaming text — it mangles partial markdown.
			// Do a lightweight pass to render headers and plain text.
			lines := strings.Split(l.Text, "\n")
			dotPlaced := false
			for _, physical := range lines {
				trimmed := strings.TrimSpace(physical)
				if strings.HasPrefix(trimmed, "#") {
					// Count leading # chars to determine heading level
					level := 0
					for _, ch := range trimmed {
						if ch == '#' {
							level++
						} else {
							break
						}
					}
					text := strings.TrimSpace(trimmed[level:])
					prefix := strings.Repeat("─", level) + " "
					rendered := streamHeadStyle.Render(prefix + text)
					if !dotPlaced {
						out = append(out, assistantStyle.Render("● ")+rendered)
						dotPlaced = true
					} else {
						out = append(out, rendered)
					}
					continue
				}
				wrapped := wrapText(physical, m.width-2)
				for _, wl := range strings.Split(wrapped, "\n") {
					if !dotPlaced && strings.TrimSpace(wl) != "" {
						out = append(out, assistantStyle.Render("● ")+wl)
						dotPlaced = true
					} else {
						out = append(out, wl)
					}
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

	header := fmt.Sprintf("━━━ %s %s ━━━", diff.Operation, diff.Path)
	out = append(out, diffFileStyle.Render(header))

	if diff.IsNewFile {
		out = append(out, diffAddMarkStyle.Render("+ new file"))
		lines := strings.Split(diff.NewContent, "\n")
		for i, ln := range lines {
			num := diffAddMarkStyle.Render(fmt.Sprintf("%4d", i+1))
			plus := diffAddMarkStyle.Render(" + ")
			code := diffCodeStyle.Render(ln)
			out = append(out, num+plus+code)
		}
	} else {
		oldLines := strings.Split(diff.OldContent, "\n")
		newLines := strings.Split(diff.NewContent, "\n")
		maxOld := len(oldLines)
		maxNew := len(newLines)

		i, j := 0, 0
		for i < maxOld || j < maxNew {
			if i < maxOld && j < maxNew && oldLines[i] == newLines[j] {
				num := diffLineNumStyle.Render(fmt.Sprintf("%4d", i+1))
				out = append(out, num+"   "+oldLines[i])
				i++
				j++
			} else if i < maxOld {
				num := diffRemMarkStyle.Render(fmt.Sprintf("%4d", i+1))
				dash := diffRemMarkStyle.Render(" - ")
				code := diffFadedStyle.Render(oldLines[i])
				out = append(out, num+dash+code)
				i++
			} else {
				num := diffAddMarkStyle.Render(fmt.Sprintf("%4d", j+1))
				plus := diffAddMarkStyle.Render(" + ")
				code := diffCodeStyle.Render(newLines[j])
				out = append(out, num+plus+code)
				j++
			}
		}
	}

	out = append(out, "")
	return out
}

// renderReadingWindow draws the animated file reading view.
// A 13-line window is centered on the cursor line.
func renderReadingWindow(r *readingFile, width int) []string {
	var out []string

	header := fmt.Sprintf(" ▶ reading  %s ", filepath.Base(r.path))
	out = append(out, diffFileStyle.Render(header))

	const windowSize = 13
	start := r.cursor - windowSize/2
	if start < 0 {
		start = 0
	}
	end := start + windowSize
	if end > len(r.lines) {
		end = len(r.lines)
		if start = end - windowSize; start < 0 {
			start = 0
		}
	}

	maxContent := width - 8
	if maxContent < 10 {
		maxContent = 10
	}

	for i := start; i < end; i++ {
		lineNum := fmt.Sprintf("%4d", i+1)
		content := r.lines[i]
		if len(content) > maxContent {
			content = content[:maxContent]
		}
		if i == r.cursor {
			out = append(out, diffLineNumStyle.Render(lineNum)+" "+readCursorStyle.Render("► "+content))
		} else {
			out = append(out, diffLineNumStyle.Render(lineNum)+"   "+readDimStyle.Render(content))
		}
	}

	progress := fmt.Sprintf("  [line %d / %d]", r.cursor+1, len(r.lines))
	out = append(out, toolStyle.Render(progress))
	return out
}

// parseReadOutput converts the line-numbered output of tools.Read ("  N|content")
// into a plain slice of content strings.
func parseReadOutput(output string) []string {
	var lines []string
	for _, l := range strings.Split(output, "\n") {
		if idx := strings.Index(l, "|"); idx >= 0 {
			lines = append(lines, l[idx+1:])
		}
	}
	return lines
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
	maxScroll := len(rendered) - m.chatRows()
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
	m.scroll = len(rendered) - m.chatRows()
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
				// Tool results are shown via stream events during turns; nothing to render here.
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
	return height - 3
}

// chatRows returns the number of rows available for chat content in chat mode.
func (m *model) chatRows() int {
	if m.height <= 4 {
		return 5
	}
	reserved := 2 // status bar + input
	if len(m.changedFiles) > 0 {
		reserved++
	}
	if m.running && m.toolStatus != "" {
		reserved++
	}
	reserved += m.slashMenuHeight()
	r := m.height - reserved
	if r < 1 {
		return 1
	}
	return r
}

func (m *model) slashMenuHeight() int {
	n := len(m.slashSuggest)
	if n == 0 {
		return 0
	}
	if n > 6 {
		n = 6
	}
	return n + 1 // items + header line
}

// upsertChangedFile adds or updates the entry for this path in changedFiles.
func (m *model) upsertChangedFile(d tools.DiffInfo) {
	for i, f := range m.changedFiles {
		if f.Path == d.Path {
			m.changedFiles[i] = d
			return
		}
	}
	m.changedFiles = append(m.changedFiles, d)
}

// updateSlashSuggest recomputes slash suggestions from the current input.
func (m *model) updateSlashSuggest() {
	if !strings.HasPrefix(m.input, "/") || strings.ContainsRune(m.input, ' ') {
		m.slashSuggest = nil
		m.slashSelIdx = 0
		return
	}
	prefix := m.input[1:]
	prev := m.slashSuggest
	m.slashSuggest = m.slashSuggest[:0]
	for i, d := range slashDefs {
		if strings.HasPrefix(d.cmd, prefix) {
			m.slashSuggest = append(m.slashSuggest, i)
		}
	}
	// Reset selection if suggestions changed
	if len(m.slashSuggest) != len(prev) {
		m.slashSelIdx = 0
	}
	if m.slashSelIdx >= len(m.slashSuggest) {
		m.slashSelIdx = 0
	}
}

// executeSlash runs a slash command by name with optional args.
func (m *model) executeSlash(cmd string, args []string) {
	switch cmd {
	case "clear":
		m.lines = nil
		m.messages = nil
		m.allToolLines = nil
		m.changedFiles = nil
		m.streamText = ""
		m.bumpCache()
		if m.session != nil {
			_ = m.session.SaveMessages(nil)
		}

	case "model":
		if len(args) == 0 {
			cur := m.modelName
			if m.modelOverride != "" {
				cur = m.modelOverride
			}
			m.lines = append(m.lines, line{Kind: "tool",
				Text: fmt.Sprintf("current model: %s\nset with: /model haiku  /model sonnet  /model opus", shortModel(cur))})
		} else {
			switch args[0] {
			case "haiku":
				m.modelOverride = provider.ModelHaiku
			case "sonnet":
				m.modelOverride = provider.ModelSonnet
			case "opus":
				m.modelOverride = provider.ModelOpus
			default:
				m.lines = append(m.lines, line{Kind: "error", Text: "unknown model: " + args[0] + "  (haiku|sonnet|opus)"})
				m.bumpCache()
				return
			}
			m.modelName = m.modelOverride
			m.lines = append(m.lines, line{Kind: "tool", Text: "switched to " + shortModel(m.modelOverride)})
		}
		m.bumpCache()

	case "usage":
		text := fmt.Sprintf(
			"session tokens  in=%d  out=%d\nest. cost  $%.4f (blended @$3/M)",
			m.totalUsage.InputTokens, m.totalUsage.OutputTokens,
			estimateCost(m.totalUsage),
		)
		m.lines = append(m.lines, line{Kind: "tool", Text: text})
		m.bumpCache()

	case "help":
		var sb strings.Builder
		for _, d := range slashDefs {
			if d.args != "" {
				sb.WriteString(fmt.Sprintf("  /%s %s — %s\n", d.cmd, d.args, d.desc))
			} else {
				sb.WriteString(fmt.Sprintf("  /%s — %s\n", d.cmd, d.desc))
			}
		}
		sb.WriteString("\n  Tab=Focus files bar  ←/→=Navigate  Enter=Open diff  Esc=Back")
		m.lines = append(m.lines, line{Kind: "tool", Text: strings.TrimRight(sb.String(), "\n")})
		m.bumpCache()

	case "diff":
		if len(m.changedFiles) == 0 {
			m.lines = append(m.lines, line{Kind: "tool", Text: "no files changed yet"})
			m.bumpCache()
		} else {
			m.mode = modeDiff
			m.diffScroll = 0
		}

	case "new":
		newSess, _, err := store.ResumeOrNew("", m.cwd, provider.DefaultModel())
		if err == nil {
			m.session = newSess
		}
		m.lines = nil
		m.messages = nil
		m.allToolLines = nil
		m.changedFiles = nil
		m.streamText = ""
		m.totalUsage = provider.Usage{}
		m.bumpCache()

	default:
		m.lines = append(m.lines, line{Kind: "error", Text: "unknown command: /" + cmd + "  (type /help)"})
		m.bumpCache()
	}
}

// renderSlashMenu renders the slash command autocomplete dropdown.
func (m *model) renderSlashMenu() string {
	var b strings.Builder
	n := len(m.slashSuggest)
	if n > 6 {
		n = 6
	}
	header := slashMenuBorderStyle.Width(max(1, m.width)).Render(" commands")
	b.WriteString(header)
	b.WriteString("\n")
	for i := 0; i < n; i++ {
		def := slashDefs[m.slashSuggest[i]]
		label := slashCmdStyle.Render("/"+def.cmd)
		if def.args != "" {
			label += " " + slashArgStyle.Render(def.args)
		}
		label += "  " + def.desc
		if i == m.slashSelIdx {
			b.WriteString(slashItemSelStyle.Width(max(1, m.width)).Render("▸ " + label))
		} else {
			b.WriteString(slashItemStyle.Width(max(1, m.width)).Render(label))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderFilesBar renders the 1-row files tab strip.
func (m *model) renderFilesBar() string {
	var b strings.Builder
	b.WriteString(fileBarBgStyle.Render(" "))
	for i, f := range m.changedFiles {
		name := filepath.Base(f.Path)
		if i == m.fileIdx && m.fileBarFocus {
			b.WriteString(fileTabActiveStyle.Render("▸ " + name))
		} else if i == m.fileIdx {
			b.WriteString(fileTabActiveStyle.Render("● " + name))
		} else {
			b.WriteString(fileTabStyle.Render("◦ " + name))
		}
		if i < len(m.changedFiles)-1 {
			b.WriteString(fileBarBgStyle.Render(" │"))
		}
	}
	// Pad to full width
	hint := ""
	if m.fileBarFocus {
		hint = "  ←/→ navigate · Enter open · Esc back"
	} else {
		hint = "  Tab to navigate"
	}
	b.WriteString(fileBarBgStyle.Render(hint))
	return fileBarBgStyle.Width(max(1, m.width)).Render(b.String())
}

// renderDiffView renders the full-screen diff view mode.
func (m *model) renderDiffView() string {
	var b strings.Builder
	if len(m.changedFiles) == 0 {
		m.mode = modeChat
		return ""
	}
	if m.fileIdx >= len(m.changedFiles) {
		m.fileIdx = len(m.changedFiles) - 1
	}

	// ── Tab bar ──────────────────────────────────────────────────────────────
	tabBar := diffTabBarStyle.Render(" ")
	for i, f := range m.changedFiles {
		name := filepath.Base(f.Path)
		if i == m.fileIdx {
			tabBar += diffTabActiveStyle.Render("● " + name)
		} else {
			tabBar += diffTabItemStyle.Render("◦ " + name)
		}
		if i < len(m.changedFiles)-1 {
			tabBar += diffTabBarStyle.Render(" │")
		}
	}
	b.WriteString(diffTabBarStyle.Width(max(1, m.width)).Render(tabBar))
	b.WriteString("\n")

	// ── Diff content ─────────────────────────────────────────────────────────
	diff := m.changedFiles[m.fileIdx]
	diffLines := renderDiff(&diff, m.width)
	contentRows := m.height - 2
	if m.diffScroll > len(diffLines)-contentRows {
		m.diffScroll = len(diffLines) - contentRows
	}
	if m.diffScroll < 0 {
		m.diffScroll = 0
	}
	end := m.diffScroll + contentRows
	if end > len(diffLines) {
		end = len(diffLines)
	}
	for i := m.diffScroll; i < end; i++ {
		b.WriteString(diffLines[i])
		b.WriteString("\n")
	}
	for i := end - m.diffScroll; i < contentRows; i++ {
		b.WriteString("\n")
	}

	// ── Hint bar ─────────────────────────────────────────────────────────────
	hint := fmt.Sprintf(" ←/→ switch file  ↑/↓ scroll  Esc return   %s/%d", filepath.Base(diff.Path), len(m.changedFiles))
	b.WriteString(diffHintStyle.Width(max(1, m.width)).Render(hint))

	return b.String()
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
		"• " + highlight.Render("Enter") + " - Send  |  " + highlight.Render("Alt+Enter") + " - New line",
		"• " + highlight.Render("/help") + " - Commands  |  " + highlight.Render("Tab") + " - Browse files",
		"• " + highlight.Render("↑/↓") + " - History  |  " + highlight.Render("PgUp/PgDn") + " - Scroll",
		"• " + highlight.Render("Ctrl+C") + " - Cancel/Quit  |  " + highlight.Render("Ctrl+D") + " - Exit",
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