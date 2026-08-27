package chat

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	openacp "github.com/yusheng-g/openagent-go/acp/sdk"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/components"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/layout"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/theme"
)

// This package implements the TUI chat page: welcome screen, input, ACP
// backend connection (in-process via io.Pipe), streaming agent responses,
// and transcript rendering.

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

	// inChat controls welcome vs chat view. Set true on first Enter so the
	// view switches immediately without waiting for the ACP session ID.
	inChat bool

	// mode is the current session mode ("auto" | "manual" | "plan"). Shown as
	// a badge in the input header; manual is the server default.
	mode string

	// logoColor / logoGradient drive the welcome-page logo coloring from
	// settings.json. Gradient (2+ stops) wins over single color.
	logoColor    string
	logoGradient []string

	messages []ChatMessage

	spinner components.Loading
	loading bool

	statusBar components.StatusBar

	turnId int64

	// ACP backend connection (injected by app.go's startACPInProcess goroutine)
	acpSession *openacp.Session
	program    *tea.Program

	// permission dialog: when non-nil, a tool call is awaiting approval.
	// The user's selection is sent back via permissionReplyCh.
	permissionReq        *openacp.RequestPermissionRequest
	permissionReplyCh    chan openacp.RequestPermissionResponse
	permissionSelectedIdx int
	permissionOptionY     []int // Y coordinates of each option row (terminal-relative)

	chatViewport viewport.Model
	chatTextarea textarea.Model

	viewportDirty bool
	textareaDirty bool

	statusText string

	totalTokens string

	needAutoScroll bool

	// input cursor blink
	blinkCount int
	blink      bool

	tips       string
	suggestion string

	modelId    string
	providerId string
}

// ChatMessage is a transcript message record.
type ChatMessage struct {
	Role    string
	Content string
	TurnId  int64
}

// NewModel builds a chat model. ver is shown in the footer/sidebar; name is
// the agent name (used for ACP client identity); mode is the initial session
// mode ("auto"|"manual"|"plan"); logoColor/logoGradient drive the welcome
// logo coloring.
func NewModel(ctx context.Context, cancel context.CancelFunc, workDir, ver, mode, logoColor string, logoGradient []string) *Model {
	if mode == "" {
		mode = "manual"
	}

	// viewport (transcript scroll area)
	vp := viewport.New()
	vp.SetWidth(layout.GetViewWidth(defaultWidth))
	vp.SetHeight(layout.GetViewHeight(defaultHeight))
	vp.FillHeight = true
	vp.Style = theme.BaseStyle()

	// textarea (input)
	ta := textarea.New()
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

		mode:      mode,
		logoColor: logoColor,
		// Default gradient: blue → purple → pink. Used when the user hasn't
		// set tui.logo_gradient or tui.colors.logo_color in settings.json.
		logoGradient: defaultLogoGradient(logoColor, logoGradient),

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

// defaultLogoGradient returns the gradient to use for the logo when the user
// hasn't configured one. Falls back to blue→purple→pink so the logo has
// color out of the box.
func defaultLogoGradient(logoColor string, logoGradient []string) []string {
	if len(logoGradient) > 0 {
		return logoGradient
	}
	if strings.TrimSpace(logoColor) != "" {
		return nil // single color mode
	}
	return []string{"#007aff", "#af52de", "#ff2d92"}
}

// SetProgram injects the tea.Program so ACP goroutines can send tea.Msg via
// Program.Send. Called by app.go after NewProgram but before Run.
func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
}

// SetACPSession injects the ACP session once the in-process backend
// connection is established. Called by startACPInProcess in app.go.
func (m *Model) SetACPSession(s *openacp.Session) {
	m.acpSession = s
}

// ── ACP tea.Msg types ──

type acpReadyMsg struct{ sessionID string }
type agentMessageMsg struct{ text string }
type agentThoughtMsg struct{ text string }
type promptDoneMsg struct{}
type acpErrorMsg struct{ err error }
type permissionRequestMsg struct {
	req     openacp.RequestPermissionRequest
	replyCh chan openacp.RequestPermissionResponse
}

// Exported constructors for app.go to send these msgs from the ACP goroutine.
func AcpReadyMsg(sessionID string) tea.Msg { return acpReadyMsg{sessionID: sessionID} }
func AcpErrorMsg(err error) tea.Msg        { return acpErrorMsg{err: err} }

func (m *Model) Init() tea.Cmd {
	return spinnerTick()
}

// Update handles window resize, spinner/blink ticks, quit, text input,
// and ACP streaming events (agent messages, prompt completion, errors).
// All model state changes happen here in the bubbletea event loop — ACP
// goroutines send tea.Msg via Program.Send (thread-safe), no locks needed.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.updateWindowSize(msg)

	case tea.FocusMsg:
		if m.focus == FocusChat {
			m.chatTextarea.Focus()
		}
		return m, nil

	case tea.BlurMsg:
		// Keep the textarea focused even on terminal blur events so the
		// TUI always accepts input.
		if m.focus == FocusChat {
			m.chatTextarea.Focus()
		}
		return m, nil

	case tea.PasteMsg:
		if m.focus == FocusChat {
			var cmd tea.Cmd
			m.chatTextarea, cmd = m.chatTextarea.Update(msg)
			return m, cmd
		}
		return m, nil

	case permissionRequestMsg:
		m.permissionReq = &msg.req
		m.permissionReplyCh = msg.replyCh
		return m, nil

	case tea.KeyMsg:
		// If permission dialog is open, intercept keys for selection.
		if m.permissionReq != nil {
			if k, ok := msg.(tea.KeyPressMsg); ok {
				switch k.String() {
				case "ctrl+c":
					return m, tea.Quit
				case "esc":
					m.respondPermission(-1)
					return m, nil
				case "up":
					if m.permissionSelectedIdx > 0 {
						m.permissionSelectedIdx--
					}
					return m, nil
				case "down":
					if m.permissionReq != nil && m.permissionSelectedIdx < len(m.permissionReq.Options)-1 {
						m.permissionSelectedIdx++
					}
					return m, nil
				case "enter":
					m.respondPermission(m.permissionSelectedIdx)
					return m, nil
				}
			}
			return m, nil
		}
		if k, ok := msg.(tea.KeyPressMsg); ok {
			switch k.String() {
			case "ctrl+c", "esc":
				return m, tea.Quit
			case "enter":
				text := m.chatTextarea.Value()
				if strings.TrimSpace(text) == "" {
					return m, nil
				}
				// User message → transcript, then async Prompt to ACP.
				m.messages = append(m.messages, ChatMessage{
					Role:    "user",
					Content: text,
					TurnId:  m.turnId,
				})
				m.chatTextarea.SetValue("")
				m.viewportDirty = true
				m.loading = true
				m.inChat = true
				if m.acpSession != nil {
					go func() {
						_, err := m.acpSession.Prompt(m.ctx, openacp.PromptRequest{
							Prompt: []openacp.ContentBlock{{Type: "text", Text: text}},
						})
						if err != nil {
							m.program.Send(acpErrorMsg{err: err})
							return
						}
						m.program.Send(promptDoneMsg{})
					}()
				}
				return m, nil
			}
		}
		// Forward all other keys to the textarea.
		if m.focus == FocusChat {
			var cmd tea.Cmd
			m.chatTextarea, cmd = m.chatTextarea.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.MouseClickMsg:
		// Click on a permission option to select it.
		if m.permissionReq != nil && msg.Button == tea.MouseLeft {
			idx := m.permissionOptionAt(msg.Y)
			if idx >= 0 {
				m.respondPermission(idx)
			}
			return m, nil
		}

	// ── ACP streaming events ──
	case acpReadyMsg:
		m.activeSessionID = msg.sessionID
		return m, nil
	case agentMessageMsg:
		if n := len(m.messages); n > 0 && m.messages[n-1].Role == "assistant" && m.messages[n-1].TurnId == m.turnId {
			m.messages[n-1].Content += msg.text
		} else {
			m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: msg.text, TurnId: m.turnId})
		}
		m.viewportDirty = true
		return m, nil
	case agentThoughtMsg:
		if n := len(m.messages); n > 0 && m.messages[n-1].Role == "thought" && m.messages[n-1].TurnId == m.turnId {
			m.messages[n-1].Content += msg.text
		} else {
			m.messages = append(m.messages, ChatMessage{Role: "thought", Content: msg.text, TurnId: m.turnId})
		}
		m.viewportDirty = true
		return m, nil
	case promptDoneMsg:
		m.loading = false
		return m, nil
	case acpErrorMsg:
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: msg.err.Error()})
		m.loading = false
		m.viewportDirty = true
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

// permissionOptionAt returns the option index for a terminal Y coordinate,
// or -1 if the click is not on an option row. Uses permissionOptionY which
// is populated during renderPermissionPanel.
func (m *Model) permissionOptionAt(y int) int {
	for i, oy := range m.permissionOptionY {
		if oy == y {
			return i
		}
	}
	return -1
}

// respondPermission sends the user's selection back to the ACP server via
// the reply channel. idx >= 0 selects option[idx]; idx < 0 cancels.
func (m *Model) respondPermission(idx int) {
	if m.permissionReq == nil || m.permissionReplyCh == nil {
		return
	}
	var resp openacp.RequestPermissionResponse
	if idx >= 0 && idx < len(m.permissionReq.Options) {
		optID := openacp.PermissionOptionId(m.permissionReq.Options[idx].OptionID)
		resp = openacp.RequestPermissionResponse{
			Outcome: openacp.RequestPermissionOutcome{
				Outcome:  "selected",
				OptionID: &optID,
			},
		}
	} else {
		resp = openacp.RequestPermissionResponse{
			Outcome: openacp.RequestPermissionOutcome{Outcome: "cancelled"},
		}
	}
	m.permissionReplyCh <- resp
	m.permissionReq = nil
	m.permissionReplyCh = nil
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

// ── tick commands ──

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

// renderMessages renders the message list as a styled transcript. Each role
// gets distinct visual treatment (background, border, color) so user input,
// agent thoughts, and agent replies are clearly separated.
func (m *Model) renderMessages() string {
	if len(m.messages) == 0 {
		return ""
	}
	vpW := layout.GetViewWidth(m.width)
	base := theme.BaseStyle().Margin(1, 0).MarginBackground(theme.BgNormal)
	var doc strings.Builder
	for _, msg := range m.messages {
		content := strings.TrimSpace(msg.Content)
		switch msg.Role {
		case "user":
			doc.WriteString(base.Padding(1).
				Width(vpW).
				Background(theme.BgSurface).
				Border(lipgloss.ThickBorder(), false, false, false, true).
				BorderBackground(theme.BgNormal).
				BorderForeground(theme.Primary).
				Render(content))
		case "assistant":
			doc.WriteString(base.Padding(1).
				Width(vpW).
				Background(theme.BgSurface).
				Foreground(theme.TextAsh).
				Render(content))
		case "thought":
			if content != "" {
				doc.WriteString(base.Width(vpW).Render(
					theme.BaseStyle().Foreground(theme.Warning).Italic(true).Render("Thinking: ") +
						theme.BaseStyle().Foreground(theme.TextStone).Render(content),
				))
			} else if m.loading {
				doc.WriteString(base.Width(vpW).Foreground(theme.Warning).Italic(true).Render("Thinking..."))
			}
		case "error":
			doc.WriteString(base.Width(vpW).Foreground(theme.Danger).Render(content))
		default:
			doc.WriteString(base.Width(vpW).Padding(0, 1).Foreground(theme.TextAsh).Render(content))
		}
	}
	return doc.String()
}
