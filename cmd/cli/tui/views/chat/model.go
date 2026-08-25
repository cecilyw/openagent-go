package chat

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/yusheng-g/openagent-go/cmd/cli/tui/components"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/layout"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/theme"
)

// This package ports the /tmp/tui welcome + chat *pages* (rendering only).
// All event/ACP/input logic from the reference was stripped: Model holds only
// the fields the View layer reads, Init returns a single spinner tick, and
// Update handles only window resize + quit. Functional wiring is deferred.

type FocusArea int

const (
	FocusChat FocusArea = iota
)

const (
	defaultWidth  = 80
	defaultHeight = 30
)

const (
	PlaceholderPrefix = "Ask anything ... e.g. "
	PlaceholderSuffix = " (Tab to accept)"
)

// Model is the chat page model. It is deliberately render-only: no ACP client,
// no event loop, no input history. NewModel takes plain parameters so the TUI
// can boot standalone without a backend.
type Model struct {
	width  int
	height int

	ctx    context.Context
	cancel context.CancelFunc

	workDir string
	version string

	focus FocusArea

	activeSessionID string

	// mode is the current session mode ("auto" | "manual" | "plan"). Mirrors
	// openacp.SessionModeId. Shown as a badge in the input header; manual is
	// the server default (acp/server.go defaultMode).
	mode string

	messages []ChatMessage

	spinner components.Loading
	loading bool

	statusBar components.StatusBar

	// 聊天交互逻辑
	chatViewport viewport.Model
	chatTextarea textarea.Model

	viewportDirty bool
	textareaDirty bool

	statusText string

	totalTokens string

	needAutoScroll bool

	// 输入框光标
	blinkCount int
	blink      bool

	tips       string
	suggestion string

	modelId    string
	providerId string
}

// ChatMessage is a minimal message record for the transcript. The reference's
// components.ChatMessage carries rendering logic we did not port; here we keep
// only what renderMessages needs. When the full message component is ported,
// this becomes components.ChatMessage.
type ChatMessage struct {
	Role    string
	Content string
	TurnId  int64
}

// NewModel builds a render-only chat model. ver is the build version shown in
// the footer/sidebar; name is reserved for future backend wiring; mode is the
// initial session mode ("auto"|"manual"|"plan") shown as the input-header
// badge. mode should already be resolved to a non-empty value by the caller
// (config.ApplyDefaults handles the fallback chain).
func NewModel(ctx context.Context, cancel context.CancelFunc, workDir, ver, name, mode string) *Model {
	_ = name // reserved for future backend wiring
	if mode == "" {
		mode = "manual"
	}

	// 滚动容器
	vp := viewport.New()
	vp.SetWidth(layout.GetViewWidth(defaultWidth))
	vp.SetHeight(layout.GetViewHeight(defaultHeight))
	vp.Style = theme.BaseStyle().Padding(0, 1)

	// 输入框
	ta := textarea.New()
	// 样式设置调整 - v2 中使用新的 Styles 方法
	styles := textarea.Styles{}
	styles.Focused.Base = theme.BaseStyle().Background(theme.BgSurface)
	styles.Focused.Placeholder = theme.BaseStyle().Background(theme.BgSurface).Foreground(theme.TextAsh)
	styles.Blurred.Base = theme.BaseStyle().Background(theme.BgSurface)
	styles.Blurred.Placeholder = theme.BaseStyle().Background(theme.BgSurface).Foreground(theme.TextAsh)

	ta.SetStyles(styles)
	suggestion := components.NextSuggestion()
	ta.Placeholder = suggestion + PlaceholderSuffix
	ta.Prompt = ""
	ta.SetWidth(defaultWidth)
	ta.SetHeight(layout.InputHeight)
	ta.CharLimit = 4096
	ta.ShowLineNumbers = false
	ta.Focus()

	return &Model{
		ctx:    ctx,
		cancel: cancel,

		workDir: workDir,
		version: ver,

		width:  defaultWidth,
		height: defaultHeight,

		activeSessionID: "",

		mode: mode,

		focus: FocusChat,

		chatTextarea: ta,
		spinner:      components.NewLoading([]string{"|", "/", "-", "\\"}),
		loading:      false,

		statusBar:    components.NewStatusBar(),
		chatViewport: vp,

		statusText: "",

		totalTokens: "0",

		needAutoScroll: true,

		modelId:    "",
		providerId: "",

		tips:       components.NextHelpTip(),
		suggestion: suggestion,
	}
}

func (m *Model) Init() tea.Cmd {
	return spinnerTick()
}

// Update handles only the minimal interactions needed to run the page: window
// resize, spinner tick, blink, and quit. Input/mouse/ACP handling is deferred.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.updateWindowSize(msg)

	case tea.KeyMsg:
		// Minimal quit binding so the TUI is escapable. Real key handling
		// (typing, send, slash commands) is out of scope for this pass.
		if k, ok := msg.(tea.KeyPressMsg); ok {
			switch k.String() {
			case "ctrl+c", "esc":
				return m, tea.Quit
			}
		}
		return m, nil

	case spinnerTickMsg:
		m.spinner = m.spinner.Tick()
		return m, spinnerTick()

	case blinkTickMsg:
		m.blink = !m.blink
		m.blinkCount++
		return m, blinkTick()
	}
	return m, nil
}

func (m *Model) updateWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.chatViewport.SetWidth(layout.GetViewWidth(m.width))
	m.chatViewport.SetHeight(layout.GetViewHeight(m.height))
	m.chatTextarea.SetWidth(layout.GetContentWidth(m.width))
	m.viewportDirty = true
	m.textareaDirty = true
	return m, nil
}

// ── tick commands (render-only subset of /tmp/tui update_tick.go) ──

type spinnerTickMsg struct {
	time time.Time
}

func spinnerTick() tea.Cmd {
	return tea.Tick(time.Millisecond*(1000/4), func(t time.Time) tea.Msg {
		return spinnerTickMsg{time: t}
	})
}

type blinkTickMsg struct {
	time time.Time
}

func blinkTick() tea.Cmd {
	return tea.Tick(time.Millisecond*530, func(t time.Time) tea.Msg {
		return blinkTickMsg{time: t}
	})
}

// renderMessages placeholder: no message source yet, so the transcript is
// empty. Kept as a method so view.go's structure matches the reference.
func (m *Model) renderMessages() string {
	var doc strings.Builder
	_ = doc
	return ""
}
