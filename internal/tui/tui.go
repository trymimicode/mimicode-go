package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	chroma "github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/trymimicode/mimicode-go/internal/agent"
	"github.com/trymimicode/mimicode-go/internal/compactor"
	"github.com/trymimicode/mimicode-go/internal/config"
	"github.com/trymimicode/mimicode-go/internal/recovery"
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
	RetryCount int
}

type tickMsg time.Time

const (
	modeChat = iota
	modeDiff
	modeSession
	modeProviderPicker
	modeModelPicker
)

type slashDef struct{ cmd, args, desc string }

var slashDefs = []slashDef{
	{"clear", "", "Clear the chat"},
	{"model", "", "Switch model (picker)"},
	{"provider", "", "Switch provider (picker)"},
	{"usage", "", "Show token usage"},
	{"help", "", "List commands"},
	{"new", "", "Start a new session"},
	{"diff", "", "Browse changed files"},
	{"sessions", "", "Browse past sessions"},
}

type line struct {
	Kind string
	Text string
	Diff *tools.DiffInfo
}

type selPos struct {
	row, col int
}

// readingFile tracks the animated "reading" state for a single file.
type readingFile struct {
	path    string
	lines   []string
	hlLines []string // syntax-highlighted version of lines
	cursor  int
	speed   int
	lineIdx int // index in m.lines where the placeholder sits
}

// pasteEntity marks a range in m.input that arrived as a multi-line paste.
// It is displayed as a pill ([pasted N lines]) and deleted atomically.
type pasteEntity struct {
	start   int    // byte offset in m.input, inclusive
	end     int    // byte offset in m.input, exclusive
	display string // e.g. "[pasted 3 lines]"
}

type model struct {
	session  *store.Session
	cwd      string
	messages []provider.Message
	lines    []line
	input        string
	scroll       int
	userScrolled bool
	width         int
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
	lineCache     []string // cached output of renderedLines()
	cacheDirty    bool     // true when lineCache must be recomputed
	lineHits      []string // parallel to lineCache; file path or "" per rendered line
	lineHitsDirty bool

	// files bar / diff view
	mode         int             // modeChat | modeDiff
	changedFiles []tools.DiffInfo // deduplicated, latest diff per path
	fileIdx      int             // selected file (files bar and diff view)
	fileBarFocus bool            // files bar has keyboard focus
	diffScroll      int  // scroll offset within diff view
	diffTypingHint  bool // show navigate-mode hint after rune key in diff view

	// slash commands
	slashSuggest []int // matching slashDefs indices for current input
	slashSelIdx  int   // which suggestion is highlighted

	// @ file picker
	atSuggest []string // matching file paths for current @fragment
	atSelIdx  int
	allFiles  []string // cached rg --files output

	// accumulated usage across all turns
	totalUsage provider.Usage

	// model/provider override (set by /model command)
	modelOverride    string
	providerOverride provider.Provider
	awaitingKey      string // non-empty = collecting API key for this env var

	// session browser
	sessionList   []store.SessionSummary
	sessionScroll int

	// provider picker
	providerList   []provider.ProviderMeta
	providerPickIdx int

	// model picker (flat list across all providers)
	modelList    []provider.ModelMeta
	modelPickIdx int

	// text selection
	selActive bool   // mouse button held
	selAnchor selPos // position where press began
	selEnd    selPos // position of current drag
	hasSel          bool   // a completed selection exists
	waitingContinue bool   // true after hitting the 25-step limit

	lastPrompt     string             // prompt from the most recent submit
	beforeMessages []provider.Message // messages snapshot before the most recent submit

	pasteEntities []pasteEntity // atomic multi-line paste ranges within m.input
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
	diffCodeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	diffFadedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	readCursorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Background(lipgloss.Color("238")).Bold(true)
	readDimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	streamHeadStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	selStyle         = lipgloss.NewStyle().Background(lipgloss.Color("24")).Foreground(lipgloss.Color("255"))

	// session browser
	sessionHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("236")).Bold(true)
	sessionRowStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("232"))
	sessionSelStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("237")).Bold(true)

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

var (
	ansiEscRe   = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	backtickRe  = regexp.MustCompile("`([^`]+)`")
	pathShapeRe = regexp.MustCompile(`[\w.\-/]+\.\w+`)
)

func RunTUI(sessionID string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, _ := config.Load()
	startModel := provider.DefaultModel()
	if cfg.DefaultModel != "" {
		startModel = cfg.DefaultModel
	}
	sess, messages, err := store.ResumeOrNew(sessionID, cwd, startModel)
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
	if cfg.DefaultModel != "" {
		m.modelOverride = cfg.DefaultModel
		m.modelName = cfg.DefaultModel
	}
	switch cfg.DefaultProvider {
	case "kimi":
		m.providerOverride = provider.Kimi
	case "minimax":
		m.providerOverride = provider.MiniMax
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
			stuck, isStuck := agent.IsStuck(msg.Err)
			if isStuck && agent.IsMaxSteps(stuck) {
				m.waitingContinue = true
				m.lines = append(m.lines, line{Kind: "tool", Text: "hit 25-step limit — type 'y' to continue or ask something new"})
			} else if isStuck {
				m.lines = append(m.lines, line{Kind: "tool", Text: "retrying…"})
				m.running = true
				ctx, cancel := context.WithCancel(context.Background())
				m.cancel = cancel
				sess := m.session
				cwd := m.cwd
				modelName := m.modelName
				lastPrompt := m.lastPrompt
				beforeMsgs := m.beforeMessages
				stuckReason := stuck.Reason
				cb := func(eventType string, data map[string]any) {
					if m.program != nil {
						m.program.Send(streamMsg{Event: eventType, Data: data})
					}
				}
				go func() {
					retryPrompt := lastPrompt
					if sess != nil {
						if diag, err := recovery.Diagnose(ctx, sess, stuckReason); err == nil {
							retryPrompt = lastPrompt + "\n\n[recovery] A previous attempt got stuck. Root cause: " + diag.WentWrong
							if diag.Instruction != "" {
								retryPrompt += " Take a different approach: " + diag.Instruction
							}
						}
					}
					next, err := agent.AgentTurn(ctx, agent.AgentConfig{
						CWD:      cwd,
						Session:  sess,
						MaxSteps: 25,
						StreamCB: cb,
						Model:    modelName,
					}, retryPrompt, beforeMsgs)
					if m.program != nil {
						m.program.Send(turnDoneMsg{Messages: next, Err: err, Usage: provider.LastUsage()})
					}
				}()
			} else {
				m.lines = append(m.lines, line{Kind: "error", Text: "error: " + msg.Err.Error()})
			}
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
			// start selection tracking; defer click handling until release
			p := selPos{msg.Y + m.scroll, msg.X}
			m.selActive = true
			m.selAnchor = p
			m.selEnd = p
			m.hasSel = false
			m.bumpCache()
		}
		if msg.Action == tea.MouseActionMotion && m.selActive {
			m.selEnd = selPos{msg.Y + m.scroll, msg.X}
			m.hasSel = m.selEnd != m.selAnchor
			m.bumpCache()
		}
		if msg.Action == tea.MouseActionRelease && msg.Button == tea.MouseButtonLeft {
			m.selActive = false
			if !m.hasSel {
				// plain click — fire existing click logic
				m.handleMouseClick(msg.X, msg.Y)
			} else {
				m.copySelectionToClipboard()
			}
			m.bumpCache()
		}
		if msg.Button == tea.MouseButtonWheelUp {
			if m.mode == modeDiff {
				m.diffScroll -= 3
				if m.diffScroll < 0 {
					m.diffScroll = 0
				}
			} else {
				m.scroll -= 3
				m.clampScroll()
				m.userScrolled = true
			}
		}
		if msg.Button == tea.MouseButtonWheelDown {
			if m.mode == modeDiff {
				m.diffScroll += 3
			} else {
				m.scroll += 3
				m.clampScroll()
				if m.isAtBottom() {
					m.userScrolled = false
				}
			}
		}
	}
	return m, nil
}

func (m *model) handleMouseClick(x, y int) {
	// Check for clickable file paths in the chat area (chat mode only).
	if m.mode == modeChat {
		hits := m.cachedLineHits()
		idx := y + m.scroll
		if idx >= 0 && idx < len(hits) && hits[idx] != "" {
			m.openFileInDiff(hits[idx])
			return
		}
	}

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

func (m *model) openFileInDiff(rel string) {
	content, err := os.ReadFile(filepath.Join(m.cwd, rel))
	if err != nil {
		return
	}
	d := tools.DiffInfo{
		Operation:  "view",
		Path:       rel,
		IsNewFile:  true,
		NewContent: string(content),
	}
	m.upsertChangedFile(d)
	for i, f := range m.changedFiles {
		if f.Path == rel {
			m.fileIdx = i
			break
		}
	}
	m.mode = modeDiff
	m.diffScroll = 0
}

func (m *model) copySelectionToClipboard() {
	rendered := m.renderedLines()
	lo, hi := m.selAnchor, m.selEnd
	if lo.row > hi.row || (lo.row == hi.row && lo.col > hi.col) {
		lo, hi = hi, lo
	}
	if lo.row < 0 {
		lo.row = 0
	}
	if hi.row >= len(rendered) {
		hi.row = len(rendered) - 1
	}
	var lines []string
	for i := lo.row; i <= hi.row; i++ {
		plain := ansiEscRe.ReplaceAllString(rendered[i], "")
		runes := []rune(plain)
		n := len(runes)
		startCol, endCol := 0, n
		if i == lo.row {
			startCol = lo.col
		}
		if i == hi.row {
			endCol = hi.col
		}
		if startCol > n {
			startCol = n
		}
		if endCol > n {
			endCol = n
		}
		lines = append(lines, string(runes[startCol:endCol]))
	}
	text := strings.Join(lines, "\n")
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("clip")
	default:
		cmd = exec.Command("xclip", "-selection", "clipboard")
	}
	cmd.Stdin = strings.NewReader(text)
	_ = cmd.Run()
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl+C with an active selection copies instead of quitting.
	if msg.Type == tea.KeyCtrlC && m.hasSel {
		m.copySelectionToClipboard()
		m.hasSel = false
		m.selActive = false
		m.bumpCache()
		return m, nil
	}
	// Any key clears an existing text selection.
	if m.hasSel || m.selActive {
		m.hasSel = false
		m.selActive = false
		m.bumpCache()
	}
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
			m.diffTypingHint = false
			if m.diffScroll > 0 {
				m.diffScroll--
			}
		case tea.KeyDown:
			m.diffTypingHint = false
			m.diffScroll++
		case tea.KeyPgUp:
			m.diffTypingHint = false
			m.diffScroll -= m.height - 2
			if m.diffScroll < 0 {
				m.diffScroll = 0
			}
		case tea.KeyPgDown:
			m.diffTypingHint = false
			m.diffScroll += m.height - 2
		default:
			if msg.Type == tea.KeyRunes {
				m.diffTypingHint = true
			}
		}
		return m, nil
	}

	// ── Session browser mode ─────────────────────────────────────────────────
	if m.mode == modeSession {
		switch msg.Type {
		case tea.KeyCtrlD:
			return m, tea.Quit
		case tea.KeyCtrlC, tea.KeyEsc:
			m.mode = modeChat
		case tea.KeyUp:
			if m.sessionScroll > 0 {
				m.sessionScroll--
			}
		case tea.KeyDown:
			if m.sessionScroll < len(m.sessionList)-1 {
				m.sessionScroll++
			}
		case tea.KeyEnter:
			if len(m.sessionList) > 0 {
				sel := m.sessionList[m.sessionScroll]
				sess, msgs, err := store.ResumeOrNew(sel.ID, m.cwd, m.modelName)
				if err == nil {
					m.session = sess
					m.messages = msgs
					m.lines = renderMessages(msgs)
				}
				m.allToolLines = nil
				m.changedFiles = nil
				m.streamText = ""
				m.totalUsage = provider.Usage{}
				m.mode = modeChat
				m.bumpCache()
				m.scrollToBottom()
			}
		}
	}

	// ── Provider picker mode ──────────────────────────────────────────────────
	if m.mode == modeProviderPicker {
		switch msg.Type {
		case tea.KeyCtrlD:
			return m, tea.Quit
		case tea.KeyCtrlC, tea.KeyEsc:
			m.mode = modeChat
		case tea.KeyUp:
			if m.providerPickIdx > 0 {
				m.providerPickIdx--
			}
		case tea.KeyDown:
			if m.providerPickIdx < len(m.providerList)-1 {
				m.providerPickIdx++
			}
		case tea.KeyEnter:
			if len(m.providerList) > 0 {
				p := m.providerList[m.providerPickIdx]
				if p.EnvVar != "" && os.Getenv(p.EnvVar) == "" {
					m.awaitingKey = p.EnvVar
					m.lines = append(m.lines, line{Kind: "tool",
						Text: "Enter " + p.EnvVar + " (will be saved to config):"})
					m.mode = modeChat
					m.bumpCache()
					m.scrollToBottom()
					return m, nil
				}
				m.applyProviderMeta(p)
				m.mode = modeChat
				m.bumpCache()
			}
		}
		return m, nil
	}

	// ── Model picker mode ──────────────────────────────────────────────────
	if m.mode == modeModelPicker {
		switch msg.Type {
		case tea.KeyCtrlD:
			return m, tea.Quit
		case tea.KeyCtrlC, tea.KeyEsc:
			m.mode = modeChat
		case tea.KeyUp:
			if m.modelPickIdx > 0 {
				m.modelPickIdx--
			}
		case tea.KeyDown:
			if m.modelPickIdx < len(m.modelList)-1 {
				m.modelPickIdx++
			}
		case tea.KeyEnter:
			if len(m.modelList) > 0 {
				mm := m.modelList[m.modelPickIdx]
				if mm.Provider.EnvVar != "" && os.Getenv(mm.Provider.EnvVar) == "" {
					m.awaitingKey = mm.Provider.EnvVar
					m.lines = append(m.lines, line{Kind: "tool",
						Text: "Enter " + mm.Provider.EnvVar + " (will be saved to config):"})
					m.mode = modeChat
					m.bumpCache()
					m.scrollToBottom()
					return m, nil
				}
				m.applyModelMeta(mm)
				m.mode = modeChat
				m.bumpCache()
			}
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
		if m.awaitingKey != "" {
			m.awaitingKey = ""
			m.input = ""
			m.cursor = 0
			m.lines = append(m.lines, line{Kind: "error", Text: "cancelled"})
			m.bumpCache()
			return m, nil
		}
		// Dismiss @ and slash menus
		m.atSuggest = nil
		m.atSelIdx = 0
		m.slashSuggest = nil
		m.slashSelIdx = 0

	case tea.KeyEnter:
		// ── API key collection mode ────────────────────────────────────────
		if m.awaitingKey != "" && !m.running {
			key := strings.TrimSpace(m.input)
			m.input = ""
			m.cursor = 0
			if key == "" {
				m.awaitingKey = ""
				m.lines = append(m.lines, line{Kind: "error", Text: "cancelled — no key entered"})
				m.bumpCache()
				return m, nil
			}
			if err := config.SaveKey(m.awaitingKey, key); err != nil {
				m.lines = append(m.lines, line{Kind: "error", Text: "save key: " + err.Error()})
				m.awaitingKey = ""
				m.bumpCache()
				return m, nil
			}
			envVar := m.awaitingKey
			m.awaitingKey = ""
			switch envVar {
			case "MOONSHOT_API_KEY":
				m.providerOverride = provider.Kimi
				m.modelOverride = provider.Kimi.DefaultModel()
			case "MINIMAX_API_KEY":
				m.providerOverride = provider.MiniMax
				m.modelOverride = provider.MiniMax.DefaultModel()
			}
			m.modelName = m.modelOverride
			saveDefaultModelProvider(m.modelOverride, m.providerOverride)
			m.lines = append(m.lines, line{Kind: "tool", Text: "key saved · switched to " + shortModel(m.modelOverride)})
			m.bumpCache()
			return m, nil
		}
		if !m.running {
			if m.showOnboarding {
				m.showOnboarding = false
			} else if len(m.atSuggest) > 0 {
				m.atCompleteSelected()
				return m, nil
			} else if len(m.slashSuggest) > 0 {
				// Execute highlighted slash command
				def := slashDefs[m.slashSuggest[m.slashSelIdx]]
				m.input = ""
				m.cursor = 0
				m.pasteEntities = nil
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
				m.pasteEntities = nil
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
			if idx := m.entityEndingAt(m.cursor); idx >= 0 {
				e := m.pasteEntities[idx]
				m.input = m.input[:e.start] + m.input[e.end:]
				m.pasteEntities = append(m.pasteEntities[:idx], m.pasteEntities[idx+1:]...)
				m.shiftEntities(e.start, -(e.end - e.start))
				m.cursor = e.start
			} else {
				left := m.input[:m.cursor-1]
				right := m.input[m.cursor:]
				m.input = left + right
				m.shiftEntities(m.cursor, -1)
				m.cursor--
			}
			m.updateSlashSuggest()
			m.updateAtSuggest()
		}

	case tea.KeyLeft:
		if !m.running && m.cursor > 0 {
			m.cursor--
			if idx := m.entityAt(m.cursor); idx >= 0 {
				m.cursor = m.pasteEntities[idx].start
			}
		}

	case tea.KeyRight:
		if !m.running && m.cursor < len(m.input) {
			m.cursor++
			if idx := m.entityAt(m.cursor); idx >= 0 {
				m.cursor = m.pasteEntities[idx].end
			}
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
		if len(m.atSuggest) > 0 {
			m.atSelIdx--
			if m.atSelIdx < 0 {
				m.atSelIdx = len(m.atSuggest) - 1
			}
		} else if len(m.slashSuggest) > 0 {
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
				m.userScrolled = true
			}
		} else if m.scroll > 0 {
			m.scroll--
			m.userScrolled = true
		}

	case tea.KeyDown:
		if len(m.atSuggest) > 0 {
			m.atSelIdx = (m.atSelIdx + 1) % len(m.atSuggest)
		} else if len(m.slashSuggest) > 0 {
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
					m.pasteEntities = nil
				}
			} else {
				m.scroll++
				m.clampScroll()
				if m.isAtBottom() {
					m.userScrolled = false
				}
			}
		} else {
			m.scroll++
			m.clampScroll()
			if m.isAtBottom() {
				m.userScrolled = false
			}
		}

	case tea.KeyPgUp:
		m.scroll -= m.chatRows()
		m.clampScroll()
		m.userScrolled = true

	case tea.KeyPgDown:
		m.scroll += m.chatRows()
		m.clampScroll()
		if m.isAtBottom() {
			m.userScrolled = false
		}

	case tea.KeyTab:
		if !m.running {
			if len(m.atSuggest) > 0 {
				m.atCompleteSelected()
				return m, nil
			} else if len(m.slashSuggest) > 0 {
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
		if !m.running && (msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace) {
			text := string(msg.Runes)
			if msg.Type == tea.KeySpace {
				text = " "
			}
			if strings.Contains(text, "\n") {
				// Multi-line paste: keep real text in m.input but record as atomic entity.
				nLines := strings.Count(text, "\n") + 1
				disp := fmt.Sprintf("[pasted %d lines]", nLines)
				m.shiftEntities(m.cursor, len(text))
				m.pasteEntities = append(m.pasteEntities, pasteEntity{
					start:   m.cursor,
					end:     m.cursor + len(text),
					display: disp,
				})
				left := m.input[:m.cursor]
				right := m.input[m.cursor:]
				m.input = left + text + right
				m.cursor += len(text)
			} else {
				m.shiftEntities(m.cursor, len(text))
				left := m.input[:m.cursor]
				right := m.input[m.cursor:]
				m.input = left + text + right
				m.cursor += len(text)
				m.updateSlashSuggest()
				m.updateAtSuggest()
			}
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

	// ── Session browser mode ─────────────────────────────────────────────────
	if m.mode == modeSession {
		return m.renderSessionBrowser()
	}

	if m.mode == modeProviderPicker {
		return m.renderProviderPicker()
	}

	if m.mode == modeModelPicker {
		return m.renderModelPicker()
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
	selLo, selHi := m.selAnchor, m.selEnd
	if selLo.row > selHi.row || (selLo.row == selHi.row && selLo.col > selHi.col) {
		selLo, selHi = selHi, selLo
	}
	for i := m.scroll; i < end; i++ {
		if (m.hasSel || m.selActive) && i >= selLo.row && i <= selHi.row {
			plain := ansiEscRe.ReplaceAllString(rendered[i], "")
			runes := []rune(plain)
			n := len(runes)
			startCol, endCol := 0, n
			if i == selLo.row {
				startCol = selLo.col
			}
			if i == selHi.row {
				endCol = selHi.col
			}
			if startCol > n {
				startCol = n
			}
			if endCol > n {
				endCol = n
			}
			before := string(runes[:startCol])
			sel := string(runes[startCol:endCol])
			after := string(runes[endCol:])
			b.WriteString(before + selStyle.Render(sel) + after)
		} else {
			b.WriteString(rendered[i])
		}
		b.WriteString("\n")
	}
	for i := end - m.scroll; i < rows; i++ {
		b.WriteString("\n")
	}

	// @ file picker menu
	if len(m.atSuggest) > 0 {
		b.WriteString(m.renderAtMenu())
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
	inputDisplay, dispCursor := m.inputDisplay()
	if m.awaitingKey != "" {
		prompt = "  key: "
		inputDisplay = strings.Repeat("*", len([]rune(m.input)))
		dispCursor = len([]rune(m.input))
	} else if m.running {
		prompt = "… "
	} else if strings.Contains(m.input, "\n") {
		prompt = "│ "
	}
	inputLines := wrapInput(inputDisplay, m.width-len(prompt)-2, dispCursor)
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
	if m.waitingContinue {
		m.waitingContinue = false
		if strings.ToLower(prompt) == "y" || strings.ToLower(prompt) == "yes" {
			prompt = "continue"
		}
	}
	
	// Add to history
	m.history = append(m.history, m.input)
	if len(m.history) > 100 {
		m.history = m.history[1:]
	}
	m.historyIdx = -1
	isFirst := len(m.messages) == 0 && m.session != nil

	m.input = ""
	m.cursor = 0
	m.pasteEntities = nil
	m.slashSuggest = nil
	m.slashSelIdx = 0
	m.atSuggest = nil
	m.atSelIdx = 0
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
	m.userScrolled = false
	m.scrollToBottom()

	m.lastPrompt = prompt
	if isFirst {
		if slug := sessionSlug(prompt); slug != "" {
			_ = m.session.Rename(store.AvailableSlug(slug))
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	before := append([]provider.Message(nil), m.messages[:len(m.messages)-1]...)
	m.beforeMessages = before
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
			Provider: m.providerOverride,
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
		if name == "bash" {
			if input, ok := msg.Data["input"].(map[string]any); ok {
				if cmd, _ := input["cmd"].(string); cmd != "" {
					l := line{Kind: "tool", Text: "$ " + cmd}
					m.lines = append(m.lines, l)
					m.allToolLines = append(m.allToolLines, l)
					m.bumpCache()
				}
			}
		}
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
			hlLines := syntaxHighlightLines(strings.Join(lines, "\n"), filepath.Base(path))
			for len(hlLines) < len(lines) {
				hlLines = append(hlLines, "")
			}
			if len(hlLines) != len(lines) {
				hlLines = lines
			}
			lineIdx := len(m.lines)
			m.lines = append(m.lines, line{Kind: "reading", Text: path})
			m.reading = &readingFile{
				path:    path,
				lines:   lines,
				hlLines: hlLines,
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
	for i := len(m.lines) - 1; i >= 0; i-- {
		if m.lines[i].Kind == "assistant_stream" {
			m.lines[i].Text = m.streamText
			m.bumpCache()
			return
		}
	}
	m.lines = append(m.lines, line{Kind: "assistant_stream", Text: m.streamText})
	m.bumpCache()
}

func (m *model) bumpCache() {
	m.cacheDirty = true
	m.lineHitsDirty = true
}

func (m *model) renderedLines() []string {
	if !m.cacheDirty && m.lineCache != nil {
		return m.lineCache
	}
	m.lineCache = m.computeRenderedLines()
	m.cacheDirty = false
	return m.lineCache
}

func (m *model) cachedLineHits() []string {
	if !m.lineHitsDirty && m.lineHits != nil {
		return m.lineHits
	}
	m.lineHits = m.computeLineHits()
	m.lineHitsDirty = false
	return m.lineHits
}

func (m *model) computeLineHits() []string {
	rendered := m.renderedLines()
	hits := make([]string, len(rendered))
	for i, rl := range rendered {
		plain := ansiEscRe.ReplaceAllString(rl, "")
		var candidates []string
		for _, match := range backtickRe.FindAllStringSubmatch(plain, -1) {
			candidates = append(candidates, match[1])
		}
		for _, p := range pathShapeRe.FindAllString(plain, -1) {
			candidates = append(candidates, p)
		}
		for _, p := range candidates {
			clean := strings.SplitN(p, ":", 2)[0]
			if _, err := os.Stat(filepath.Join(m.cwd, clean)); err == nil {
				hits[i] = clean
				break
			}
		}
	}
	return hits
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
			rendered := renderMarkdown(l.Text, m.width)
			rendered = strings.TrimLeft(rendered, "\n")
			streamLines := strings.Split(rendered, "\n")
			dotPlaced := false
			for _, physical := range streamLines {
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

	header := fmt.Sprintf("━━━ %s %s ━━━", diff.Operation, diff.Path)
	out = append(out, diffFileStyle.Render(header))

	filename := filepath.Base(diff.Path)

	if diff.IsNewFile {
		out = append(out, diffAddMarkStyle.Render("+ new file"))
		plainLines := diffSplitLines(diff.NewContent)
		hlLines := syntaxHighlightLines(diff.NewContent, filename)
		if len(hlLines) != len(plainLines) {
			hlLines = plainLines
		}
		for i, ln := range hlLines {
			num := diffAddMarkStyle.Render(fmt.Sprintf("%4d", i+1))
			plus := diffAddMarkStyle.Render(" + ")
			out = append(out, num+plus+ln)
		}
	} else {
		const contextLines = 3

		oldLines := diffSplitLines(diff.OldContent)
		newLines := diffSplitLines(diff.NewContent)
		oldHL := syntaxHighlightLines(diff.OldContent, filename)
		newHL := syntaxHighlightLines(diff.NewContent, filename)
		if len(oldHL) != len(oldLines) {
			oldHL = oldLines
		}
		if len(newHL) != len(newLines) {
			newHL = newLines
		}

		// LCS walk — build a full row list before emitting anything.
		type drow struct {
			kind    string // "ctx", "add", "rem"
			oldNum  int
			newNum  int
			content string
		}
		var rows []drow
		common := diffLCS(oldLines, newLines)
		i, j, k := 0, 0, 0
		for i < len(oldLines) || j < len(newLines) {
			switch {
			case k < len(common) && i < len(oldLines) && j < len(newLines) &&
				oldLines[i] == common[k] && newLines[j] == common[k]:
				rows = append(rows, drow{"ctx", i + 1, j + 1, newHL[j]})
				i++
				j++
				k++
			case i < len(oldLines) && (k >= len(common) || oldLines[i] != common[k]):
				rows = append(rows, drow{"rem", i + 1, 0, diffLineBg(oldHL[i], "\x1b[48;5;236m")})
				i++
			default:
				rows = append(rows, drow{"add", 0, j + 1, newHL[j]})
				j++
			}
		}

		// Mark rows within contextLines of any change as visible.
		visible := make([]bool, len(rows))
		for idx, r := range rows {
			if r.kind != "ctx" {
				lo := idx - contextLines
				if lo < 0 {
					lo = 0
				}
				hi := idx + contextLines
				if hi >= len(rows) {
					hi = len(rows) - 1
				}
				for x := lo; x <= hi; x++ {
					visible[x] = true
				}
			}
		}

		// Emit hunks; insert an @@ header each time a new visible run starts.
		inHunk := false
		for idx, r := range rows {
			if !visible[idx] {
				inHunk = false
				continue
			}
			if !inHunk {
				ref := r.newNum
				if ref == 0 {
					ref = r.oldNum
				}
				out = append(out, diffFadedStyle.Render(fmt.Sprintf("@@ line %d @@", ref)))
				inHunk = true
			}
			switch r.kind {
			case "ctx":
				num := diffLineNumStyle.Render(fmt.Sprintf("%4d", r.newNum))
				out = append(out, num+"   "+r.content)
			case "rem":
				num := diffRemMarkStyle.Render(fmt.Sprintf("%4d", r.oldNum))
				dash := diffRemMarkStyle.Render(" - ")
				out = append(out, num+dash+r.content)
			case "add":
				num := diffAddMarkStyle.Render(fmt.Sprintf("%4d", r.newNum))
				plus := diffAddMarkStyle.Render(" + ")
				out = append(out, num+plus+r.content)
			}
		}
	}

	out = append(out, "")
	return out
}

// syntaxHighlightLines runs content through chroma using the lexer matched to
// filename and returns the output split into terminal-coloured lines.
// Falls back to diffSplitLines on any error.
func syntaxHighlightLines(content, filename string) []string {
	content = strings.ReplaceAll(content, "\t", "    ")
	lx := lexers.Match(filename)
	if lx == nil {
		lx = lexers.Fallback
	}
	lx = chroma.Coalesce(lx)
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	fmt := formatters.Get("terminal256")
	if fmt == nil {
		return diffSplitLines(content)
	}
	var buf bytes.Buffer
	it, err := lx.Tokenise(nil, content)
	if err != nil {
		return diffSplitLines(content)
	}
	if err := fmt.Format(&buf, style, it); err != nil {
		return diffSplitLines(content)
	}
	return diffSplitLines(buf.String())
}

// diffLineBg re-applies bgCode after every ANSI reset in s so a background
// colour persists across chroma token boundaries.
func diffLineBg(s, bgCode string) string {
	const reset = "[0m"
	return bgCode + strings.ReplaceAll(s, reset, reset+bgCode) + reset
}

// diffSplitLines normalizes CRLF/CR to LF and trims a single trailing newline
// before splitting, so a file that ends in "\n" does not produce a spurious
// empty line at the bottom of the diff.
func diffSplitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// diffLCS returns the longest common subsequence of a and b (line-level),
// used to align the old and new versions of a file in the diff view.
func diffLCS(a, b []string) []string {
	m, n := len(a), len(b)
	if m == 0 || n == 0 {
		return nil
	}
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	result := make([]string, 0, dp[m][n])
	i, j := m, n
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			result = append(result, a[i-1])
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			i--
		} else {
			j--
		}
	}
	for l, r := 0, len(result)-1; l < r; l, r = l+1, r-1 {
		result[l], result[r] = result[r], result[l]
	}
	return result
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
		hl := r.hlLines[i]
		plain := r.lines[i]
		if len(plain) > maxContent {
			// truncate by rune count on the plain version; use plain as fallback
			hl = plain[:maxContent]
			plain = plain[:maxContent]
		}
		if i == r.cursor {
			out = append(out, diffLineNumStyle.Render(lineNum)+" "+readCursorStyle.Render("► ")+hl)
		} else {
			out = append(out, diffLineNumStyle.Render(lineNum)+"   "+hl)
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
			lines = append(lines, strings.TrimRight(l[idx+1:], "\r"))
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
		glamour.WithStylesFromJSONBytes([]byte(`{"code":{"prefix":" ","suffix":" ","color":"75","background_color":null},"code_block":{"chroma":{"error":{"color":"#7AABFF","background_color":null}}}}`)),
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

// entityAt returns the index of the paste entity that strictly contains cursor
// (start < cursor < end). Returns -1 if none.
func (m *model) entityAt(cursor int) int {
	for i, e := range m.pasteEntities {
		if e.start < cursor && cursor < e.end {
			return i
		}
	}
	return -1
}

// entityEndingAt returns the index of the paste entity whose end equals cursor.
// Returns -1 if none. Used by backspace to delete the entity atomically.
func (m *model) entityEndingAt(cursor int) int {
	for i, e := range m.pasteEntities {
		if e.end == cursor {
			return i
		}
	}
	return -1
}

// shiftEntities adjusts the start/end of every paste entity whose start is >= from
// by delta bytes. Call after inserting or deleting plain text at position from.
func (m *model) shiftEntities(from, delta int) {
	for i := range m.pasteEntities {
		if m.pasteEntities[i].start >= from {
			m.pasteEntities[i].start += delta
			m.pasteEntities[i].end += delta
		}
	}
}

// inputDisplay returns the display version of m.input (entity ranges replaced with
// their pill text) and the corresponding cursor position within that display string.
func (m *model) inputDisplay() (string, int) {
	if len(m.pasteEntities) == 0 {
		return m.input, m.cursor
	}
	entities := make([]pasteEntity, len(m.pasteEntities))
	copy(entities, m.pasteEntities)
	sort.Slice(entities, func(i, j int) bool { return entities[i].start < entities[j].start })

	var b strings.Builder
	rawOff := 0
	for _, e := range entities {
		b.WriteString(m.input[rawOff:e.start])
		b.WriteString(e.display)
		rawOff = e.end
	}
	b.WriteString(m.input[rawOff:])
	display := b.String()

	// Map m.cursor (raw) → display cursor offset.
	rawOff = 0
	dispOff := 0
	for _, e := range entities {
		if m.cursor <= e.start {
			return display, dispOff + (m.cursor - rawOff)
		}
		dispOff += (e.start - rawOff) + len(e.display)
		rawOff = e.end
		if m.cursor <= e.end {
			return display, dispOff
		}
	}
	return display, dispOff + (m.cursor - rawOff)
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

func (m *model) isAtBottom() bool {
	rendered := m.renderedLines()
	maxScroll := len(rendered) - m.chatRows()
	if maxScroll < 0 {
		maxScroll = 0
	}
	return m.scroll >= maxScroll
}

func (m *model) scrollToBottom() {
	if m.userScrolled {
		return
	}
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
	case provider.Kimi.DefaultModel():
		return "kimi"
	case provider.MiniMax.DefaultModel():
		return "minimax"
	case "":
		return "-"
	default:
		return model
	}
}

func saveDefaultModelProvider(modelOverride string, prov provider.Provider) {
	cfg, err := config.Load()
	if err != nil {
		cfg = config.Config{}
	}
	cfg.DefaultModel = modelOverride
	if prov == provider.Kimi {
		cfg.DefaultProvider = "kimi"
	} else if prov == provider.MiniMax {
		cfg.DefaultProvider = "minimax"
	} else {
		cfg.DefaultProvider = ""
	}
	_ = config.Save(cfg)
}

func sessionSlug(prompt string) string {
	words := strings.Fields(strings.ToLower(prompt))
	if len(words) > 6 {
		words = words[:6]
	}
	var parts []string
	for _, w := range words {
		var b strings.Builder
		for _, r := range []rune(w) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		if s := b.String(); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	slug := strings.Join(parts, "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	return slug
}

func estimateCost(u provider.Usage) float64 {
	// Rough blended display-only estimate until model-specific accounting lands.
	return float64(u.InputTokens+u.OutputTokens) / 1_000_000 * 3.0
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
	reserved += m.atMenuHeight()
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

// atFragmentAtCursor returns the start index and text of the @word immediately
// before the cursor, or (-1, "") if the cursor is not inside an @fragment.
func (m *model) atFragmentAtCursor() (start int, fragment string) {
	left := m.input[:m.cursor]
	at := strings.LastIndex(left, "@")
	if at == -1 {
		return -1, ""
	}
	frag := left[at+1:]
	if strings.ContainsAny(frag, " \t\n") {
		return -1, ""
	}
	return at, frag
}

// updateAtSuggest recomputes @ file suggestions from the current input.
func (m *model) updateAtSuggest() {
	start, frag := m.atFragmentAtCursor()
	if start == -1 {
		m.atSuggest = nil
		m.atSelIdx = 0
		return
	}
	if len(m.allFiles) == 0 {
		m.loadAllFiles()
	}
	lower := strings.ToLower(frag)
	prev := len(m.atSuggest)
	m.atSuggest = m.atSuggest[:0]
	for _, f := range m.allFiles {
		if strings.Contains(strings.ToLower(f), lower) {
			m.atSuggest = append(m.atSuggest, f)
			if len(m.atSuggest) >= 50 {
				break
			}
		}
	}
	if len(m.atSuggest) != prev {
		m.atSelIdx = 0
	}
	if m.atSelIdx >= len(m.atSuggest) {
		m.atSelIdx = 0
	}
}

// loadAllFiles populates m.allFiles via rg --files in the project root.
func (m *model) loadAllFiles() {
	cmd := exec.Command("rg", "--files")
	cmd.Dir = m.cwd
	out, _ := cmd.Output()
	if len(out) == 0 {
		return
	}
	m.allFiles = strings.Split(strings.TrimRight(string(out), "\n"), "\n")
}

// atCompleteSelected replaces the @fragment under the cursor with the selected path.
func (m *model) atCompleteSelected() {
	if len(m.atSuggest) == 0 {
		return
	}
	start, _ := m.atFragmentAtCursor()
	if start == -1 {
		return
	}
	path := m.atSuggest[m.atSelIdx]
	right := m.input[m.cursor:]
	m.input = m.input[:start] + "@" + path + right
	m.cursor = start + 1 + len(path)
	m.atSuggest = nil
	m.atSelIdx = 0
}

func (m *model) atMenuHeight() int {
	n := len(m.atSuggest)
	if n == 0 {
		return 0
	}
	if n > 8 {
		n = 8
	}
	return n + 1 // items + header line
}

func (m *model) renderAtMenu() string {
	var b strings.Builder
	n := len(m.atSuggest)
	if n > 8 {
		n = 8
	}
	header := slashMenuBorderStyle.Width(max(1, m.width)).Render(" files")
	b.WriteString(header)
	b.WriteString("\n")
	for i := 0; i < n; i++ {
		path := m.atSuggest[i]
		dir := filepath.Dir(path)
		base := filepath.Base(path)
		label := slashArgStyle.Render(dir+string(filepath.Separator)) + slashCmdStyle.Render(base)
		if i == m.atSelIdx {
			b.WriteString(slashItemSelStyle.Width(max(1, m.width)).Render("▸ " + label))
		} else {
			b.WriteString(slashItemStyle.Width(max(1, m.width)).Render(label))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// updateSlashSuggest recomputes slash suggestions from the current input.
func (m *model) updateSlashSuggest() {
	if m.awaitingKey != "" {
		m.slashSuggest = nil
		return
	}
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
			m.modelList = provider.Models()
			cur := m.modelName
			if m.modelOverride != "" {
				cur = m.modelOverride
			}
			for i, mm := range m.modelList {
				if mm.FullName == cur {
					m.modelPickIdx = i
					break
				}
			}
			m.mode = modeModelPicker
		} else {
			mm, ok := findModelMetaBySlug(args[0])
			if !ok {
				m.lines = append(m.lines, line{Kind: "error", Text: "unknown model: " + args[0]})
				m.bumpCache()
				return
			}
			if mm.Provider.EnvVar != "" && os.Getenv(mm.Provider.EnvVar) == "" {
				m.awaitingKey = mm.Provider.EnvVar
				m.lines = append(m.lines, line{Kind: "tool",
					Text: "Enter " + mm.Provider.EnvVar + " (will be saved to config):"})
				m.bumpCache()
				return
			}
			m.applyModelMeta(mm)
		}
		m.bumpCache()

	case "provider":
		if len(args) == 0 {
			m.providerList = provider.Providers()
			curID := currentProviderID(m.providerOverride)
			for i, p := range m.providerList {
				if p.ID == curID {
					m.providerPickIdx = i
					break
				}
			}
			m.mode = modeProviderPicker
		} else {
			p, ok := findProviderMetaByID(args[0])
			if !ok {
				m.lines = append(m.lines, line{Kind: "error", Text: "unknown provider: " + args[0]})
				m.bumpCache()
				return
			}
			if p.EnvVar != "" && os.Getenv(p.EnvVar) == "" {
				m.awaitingKey = p.EnvVar
				m.lines = append(m.lines, line{Kind: "tool",
					Text: "Enter " + p.EnvVar + " (will be saved to config):"})
				m.bumpCache()
				return
			}
			m.applyProviderMeta(p)
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

	case "sessions":
		list, err := store.ListSessions()
		if err != nil {
			m.lines = append(m.lines, line{Kind: "error", Text: "sessions: " + err.Error()})
			m.bumpCache()
			return
		}
		if len(list) == 0 {
			m.lines = append(m.lines, line{Kind: "tool", Text: "no sessions found"})
			m.bumpCache()
			return
		}
		m.sessionList = list
		m.sessionScroll = 0
		m.mode = modeSession

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
	if m.diffTypingHint {
		hint = " navigate mode — typing is disabled  ↑/↓ scroll  ←/→ switch file  Esc to return"
	}
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

// renderSessionBrowser renders the full-screen session picker.
func (m *model) renderSessionBrowser() string {
	var b strings.Builder

	header := sessionHeaderStyle.Width(m.width).Render(" sessions   ↑/↓ scroll · Enter load · Esc back")
	b.WriteString(header)
	b.WriteString("\n")

	bodyRows := m.height - 2 // header + footer
	if bodyRows < 1 {
		bodyRows = 1
	}

	// Keep selected row visible: compute scroll offset.
	start := m.sessionScroll - bodyRows/2
	if start < 0 {
		start = 0
	}
	if start+bodyRows > len(m.sessionList) {
		start = len(m.sessionList) - bodyRows
		if start < 0 {
			start = 0
		}
	}
	end := start + bodyRows
	if end > len(m.sessionList) {
		end = len(m.sessionList)
	}

	for i := start; i < end; i++ {
		s := m.sessionList[i]
		date := s.StartedAt.Format("2006-01-02 15:04")
		mod := shortModel(s.Model)
		if mod == s.Model {
			runes := []rune(s.Model)
			if len(runes) > 8 {
				mod = string(runes[:8])
			}
		}
		id := s.ID
		if len([]rune(id)) > 26 {
			id = string([]rune(id)[:26])
		}
		preview := s.Preview
		if preview == "" {
			preview = "(no messages)"
		}
		preview = strings.ReplaceAll(preview, "\n", " ")

		row := fmt.Sprintf(" %-26s  %s  %-6s  %s", id, date, mod, preview)
		runes := []rune(row)
		if len(runes) > m.width {
			row = string(runes[:m.width-1]) + "…"
		}

		if i == m.sessionScroll {
			b.WriteString(sessionSelStyle.Width(m.width).Render(row))
		} else {
			b.WriteString(sessionRowStyle.Width(m.width).Render(row))
		}
		b.WriteString("\n")
	}

	// Pad remaining rows so the footer lands at the bottom.
	for rendered := end - start; rendered < bodyRows; rendered++ {
		b.WriteString(sessionRowStyle.Width(m.width).Render(""))
		b.WriteString("\n")
	}

	footer := fmt.Sprintf(" %d / %d", m.sessionScroll+1, len(m.sessionList))
	b.WriteString(sessionHeaderStyle.Width(m.width).Render(footer))

	return b.String()
}

// applyProviderMeta switches the model to a provider's default model.
func (m *model) applyProviderMeta(p provider.ProviderMeta) {
	m.providerOverride = p.Prov
	if p.Prov == nil {
		m.modelOverride = provider.DefaultModel()
	} else {
		m.modelOverride = p.Prov.DefaultModel()
	}
	m.modelName = m.modelOverride
	saveDefaultModelProvider(m.modelOverride, m.providerOverride)
	m.lines = append(m.lines, line{Kind: "tool",
		Text: "switched to " + p.Label + " · " + shortModel(m.modelOverride)})
	m.bumpCache()
}

// applyModelMeta switches to a specific model on a specific provider.
func (m *model) applyModelMeta(mm provider.ModelMeta) {
	m.providerOverride = mm.Provider.Prov
	m.modelOverride = mm.FullName
	m.modelName = mm.FullName
	saveDefaultModelProvider(mm.FullName, mm.Provider.Prov)
	m.lines = append(m.lines, line{Kind: "tool",
		Text: "switched to " + mm.Provider.Label + " · " + shortModel(mm.FullName)})
	m.bumpCache()
}

// currentProviderID maps the active provider override back to a catalog id.
func currentProviderID(p provider.Provider) string {
	switch p {
	case provider.Kimi:
		return "kimi"
	case provider.MiniMax:
		return "minimax"
	default:
		return "claude"
	}
}

func findProviderMetaByID(id string) (provider.ProviderMeta, bool) {
	for _, p := range provider.Providers() {
		if p.ID == id {
			return p, true
		}
	}
	return provider.ProviderMeta{}, false
}

func findModelMetaBySlug(slug string) (provider.ModelMeta, bool) {
	for _, mm := range provider.Models() {
		if mm.ID == slug {
			return mm, true
		}
	}
	return provider.ModelMeta{}, false
}

// renderProviderPicker renders the full-screen provider picker.
func (m *model) renderProviderPicker() string {
	if m.providerList == nil {
		m.providerList = provider.Providers()
	}

	var b strings.Builder
	header := sessionHeaderStyle.Width(m.width).Render(" providers   ↑/↓ scroll · Enter select · Esc back")
	b.WriteString(header)
	b.WriteString("\n")

	bodyRows := m.height - 2
	if bodyRows < 1 {
		bodyRows = 1
	}

	curID := currentProviderID(m.providerOverride)
	for i, p := range m.providerList {
		row := fmt.Sprintf(" %-10s", p.Label)
		if p.ID == curID {
			row += "  ← current"
		}
		if p.EnvVar != "" && os.Getenv(p.EnvVar) == "" {
			row += fmt.Sprintf("  [needs %s]", p.EnvVar)
		}
		if len([]rune(row)) > m.width {
			row = string([]rune(row)[:max(1, m.width-1)]) + "…"
		}
		if i == m.providerPickIdx {
			b.WriteString(sessionSelStyle.Width(m.width).Render(row))
		} else {
			b.WriteString(sessionRowStyle.Width(m.width).Render(row))
		}
		b.WriteString("\n")
	}

	for rendered := len(m.providerList); rendered < bodyRows; rendered++ {
		b.WriteString(sessionRowStyle.Width(m.width).Render(""))
		b.WriteString("\n")
	}

	footer := fmt.Sprintf(" %d / %d", m.providerPickIdx+1, len(m.providerList))
	b.WriteString(sessionHeaderStyle.Width(m.width).Render(footer))

	return b.String()
}

// renderModelPicker renders the full-screen flat model picker.
func (m *model) renderModelPicker() string {
	if m.modelList == nil {
		m.modelList = provider.Models()
	}

	var b strings.Builder
	header := sessionHeaderStyle.Width(m.width).Render(" models   ↑/↓ scroll · Enter select · Esc back")
	b.WriteString(header)
	b.WriteString("\n")

	bodyRows := m.height - 2
	if bodyRows < 1 {
		bodyRows = 1
	}

	curFull := m.modelName
	if curFull == "" && m.providerOverride != nil {
		curFull = m.providerOverride.DefaultModel()
	}
	for i, mm := range m.modelList {
		row := fmt.Sprintf(" %-10s  %s", mm.Provider.Label, mm.FullName)
		if mm.FullName == curFull {
			row += "  ← current"
		}
		if mm.Provider.EnvVar != "" && os.Getenv(mm.Provider.EnvVar) == "" {
			row += fmt.Sprintf("  [needs %s]", mm.Provider.EnvVar)
		}
		if len([]rune(row)) > m.width {
			row = string([]rune(row)[:max(1, m.width-1)]) + "…"
		}
		if i == m.modelPickIdx {
			b.WriteString(sessionSelStyle.Width(m.width).Render(row))
		} else {
			b.WriteString(sessionRowStyle.Width(m.width).Render(row))
		}
		b.WriteString("\n")
	}

	for rendered := len(m.modelList); rendered < bodyRows; rendered++ {
		b.WriteString(sessionRowStyle.Width(m.width).Render(""))
		b.WriteString("\n")
	}

	footer := fmt.Sprintf(" %d / %d", m.modelPickIdx+1, len(m.modelList))
	b.WriteString(sessionHeaderStyle.Width(m.width).Render(footer))

	return b.String()
}
