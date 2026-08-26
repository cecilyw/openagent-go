package chat

import (
	"fmt"
	"image/color"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yusheng-g/openagent-go/cmd/cli/tui/components"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/layout"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/theme"
)

func createView(text string) tea.View {
	v := tea.NewView(text)
	v.AltScreen = true
	v.ReportFocus = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *Model) View() tea.View {
	if m.width <= 0 || m.height <= 0 {
		return createView("Initializing...")
	}

	if m.width < layout.MinWidth || m.height < layout.MinHeight {
		return createView(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			fmt.Sprintf("The terminal size must be greater than %d x %d. \r\nCurrent is %d x %d", layout.MinWidth, layout.MinHeight, m.width, m.height)))
	}

	var background string
	if !m.inChat {
		background = m.renderWelcome()
	} else {
		background = m.renderMainView()
	}

	return createView(background)
}

func (m *Model) renderMainView() string {
	left := m.renderLeft()
	right := m.renderRight()
	content := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(content)
}

func (m *Model) renderLeft() string {
	leftW := layout.GetLeftWidth(m.width)
	vpH := layout.GetViewHeight(m.height)
	if m.viewportDirty {
		m.chatViewport.SetContent(m.renderMessages())
		m.chatViewport.GotoBottom()
		m.viewportDirty = false
	}
	sb := m.renderScrollbar(vpH)
	scrollContainer := lipgloss.JoinHorizontal(lipgloss.Top, m.chatViewport.View(), sb)
	input := m.renderInput()
	status := m.renderStatus()
	return lipgloss.NewStyle().Width(leftW).Padding(0, 1).Render(
		lipgloss.JoinVertical(lipgloss.Left, scrollContainer, "", input, status),
	)
}

func createHalf(width int, borderColor color.Color) string {
	halfBlock := lipgloss.NewStyle().
		Background(theme.BgNormal).
		Foreground(theme.BgSurface).
		Render("▀")

	content := lipgloss.NewStyle().
		Background(theme.BgNormal).
		Foreground(borderColor).
		Render("╹")
	for i := 0; i < width-1; i++ {
		content += halfBlock
	}

	return content

}

func (m *Model) renderInput() string {
	contentWidth := m.getContentWidth()
	borderColor := theme.BorderGray
	isFocused := m.chatTextarea.Focused()
	if isFocused {
		borderColor = theme.Primary
	}
	headerStyle := theme.BaseStyle().Width(contentWidth).Height(1).Background(theme.BgSurface).PaddingLeft(1)
	contentStyle := theme.BaseStyle().Width(contentWidth).Height(layout.InputHeight).Background(theme.BgSurface).PaddingLeft(1)
	content := ""
	if m.chatTextarea.Value() == "" {
		placeholder := m.chatTextarea.Placeholder
		if len(placeholder) == 0 {
			content = contentStyle.Foreground(theme.TextAsh).Render("")
		} else if !m.blink || !isFocused {
			content = contentStyle.Foreground(theme.TextAsh).Render(placeholder)
		} else {
			firstRune, firstSize := utf8.DecodeRuneInString(placeholder)
			firstChar := theme.BaseStyle().Background(theme.TextNormal).Foreground(theme.TextInk).Render(string(firstRune))
			remainingText := ""
			if len(placeholder) > firstSize {
				remainingText = theme.BaseStyle().Background(theme.BgSurface).Foreground(theme.TextAsh).Render(placeholder[firstSize:])
			}
			content = contentStyle.Render(lipgloss.JoinHorizontal(lipgloss.Left, firstChar, remainingText))
		}

	} else {
		content = contentStyle.Render(m.chatTextarea.View())
	}
	header := headerStyle.Render("")

	info := headerStyle.PaddingLeft(1).Render(m.renderPermissionBadge() + components.RenderCommandTipSurface(m.modelId, m.providerId))
	footer := createHalf(contentWidth, borderColor)
	input := theme.BaseStyle().
		Width(contentWidth).
		Background(theme.BgSurface).
		Border(lipgloss.ThickBorder(), false, false, false, true).
		BorderBackground(theme.BgNormal).
		BorderForeground(borderColor).Render(lipgloss.JoinVertical(lipgloss.Left, header, content, info))
	return lipgloss.JoinVertical(lipgloss.Left, input, footer)
}

func (m *Model) renderPermissionBadge() string {
	var col color.Color
	switch m.mode {
	case "auto":
		col = theme.Warning
	case "plan":
		col = theme.Primary
	default:
		col = theme.Success
	}
	return theme.BaseStyle().Background(theme.BgSurface).Foreground(col).Render(strings.ToUpper(m.mode))
}

func (m *Model) renderStatus() string {
	contentWidth := m.getContentWidth()
	help := components.RenderCommandTip("enter", "send")
	help = help + components.RenderCommandTip("ctrl+c", "quit")
	help = help + components.RenderCommandTip("ctrl+p", "commands")
	m.statusBar.Width = contentWidth
	if m.loading {
		m.statusBar.Status = m.spinner.View() + " " + m.statusText
	} else {
		m.statusBar.Status = m.statusText
	}
	m.statusBar.Help = help
	return m.statusBar.View()
}

func (m *Model) renderRight() string {
	width := layout.GetRightWidth(m.width)
	if width <= 0 {
		return ""
	}
	background := theme.BaseStyle().Background(theme.BgSecondary)
	rightStyle := background.Width(width).Height(m.height).PaddingLeft(1)

	sessionTitle := background.Width(width - 1).Foreground(theme.TextNormal).Bold(true).Render("Session")
	sessionValue := background.Width(width - 1).Foreground(theme.TextAsh).Render(m.activeSessionID)

	contextTitle := background.Width(width - 1).Foreground(theme.TextNormal).Bold(true).Render("Context")
	contextValue := background.Width(width - 1).Foreground(theme.TextAsh).Render(m.totalTokens + " tokens")

	todoContent := background.Width(width - 1).Render("")
	header := lipgloss.JoinVertical(lipgloss.Left,
		sessionTitle, sessionValue, "",
		contextTitle, contextValue, "",
		todoContent,
	)

	workDirTitle := background.Width(width - 1).Foreground(theme.TextNormal).Bold(true).Render("WorkDir")
	workDirValue := background.Width(width - 1).Foreground(theme.TextAsh).Render(m.workDir)

	versionTitle := background.Width(width - 1).Foreground(theme.TextNormal).Bold(true).Render("Version")
	versionValue := background.Width(width - 1).Foreground(theme.TextAsh).Render(m.version)
	footer := lipgloss.JoinVertical(lipgloss.Left, workDirTitle, workDirValue, "", versionTitle, versionValue)

	headerH := lipgloss.Height(header)
	footerH := lipgloss.Height(footer)

	spacesH := max(0, m.height-headerH-footerH-1)
	space := strings.Repeat("\n", spacesH)
	return rightStyle.
		Render(lipgloss.JoinVertical(lipgloss.Left, header, space, footer))
}

func (m *Model) renderScrollbar(height int) string {
	if height <= 0 {
		return ""
	}
	thumbStyle := theme.BaseStyle().Background(theme.ThumbBackGround)
	trackStyle := theme.BaseStyle().Background(theme.TrackBackGround)

	emptyLine := trackStyle.Render(" ")
	trackLine := trackStyle.Render(" ")
	thumbLine := thumbStyle.Render(" ")

	totalLines := m.chatViewport.TotalLineCount()
	yOffset := m.chatViewport.YOffset

	var doc strings.Builder
	if totalLines <= height {
		for i := 0; i < height; i++ {
			doc.WriteString(emptyLine)
			if i < height-1 {
				doc.WriteString("\n")
			}
		}
		return doc.String()
	}
	thumbH := max(1, height*height/totalLines)
	maxOffset := totalLines - height
	thumbY := yOffset() * (height - thumbH) / maxOffset
	for i := 0; i < height; i++ {
		if i >= thumbY && i < thumbY+thumbH {
			doc.WriteString(thumbLine)
		} else {
			doc.WriteString(trackLine)
		}
		if i < height-1 {
			doc.WriteString("\n")
		}
	}
	return doc.String()
}
