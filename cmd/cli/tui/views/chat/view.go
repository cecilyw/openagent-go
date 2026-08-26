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
	if m.activeSessionID == "" {
		background = m.renderWelcome()
	} else {
		background = m.renderMainView()
	}

	// Panel overlays (command/help/session/model/skill/permission/slash/notify)
	// are not ported in this render-only pass. The background page stands alone.
	return createView(background)
}

func (m *Model) renderMainView() string {
	left := m.renderLeft()
	right := m.renderRight()
	content := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	return theme.BaseStyle().Width(m.width).Height(m.height).Render(content)
}

func (m *Model) renderLeft() string {
	leftW := layout.GetLeftWidth(m.width)
	vpH := layout.GetViewHeight(m.height)
	// Sync viewport content when messages changed.
	if m.viewportDirty {
		m.chatViewport.SetContent(m.renderMessages())
		m.chatViewport.GotoBottom()
		m.viewportDirty = false
	}
	sb := m.renderScrollbar(vpH)
	// 上方滚动内容区域 + 滚动条
	scrollContainer := lipgloss.JoinHorizontal(lipgloss.Top, m.chatViewport.View(), sb)
	// input
	input := m.renderInput()
	// status
	status := m.renderStatus()
	return theme.BaseStyle().Width(leftW).Padding(0, 1).Render(lipgloss.JoinVertical(lipgloss.Left, scrollContainer, "", input, status))
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

	// 添加边框样式
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
			// 渲染第一个字符(带背景色) - 使用 utf8.DecodeRuneInString 正确处理多字节字符(如中文)
			firstRune, firstSize := utf8.DecodeRuneInString(placeholder)
			firstChar := theme.BaseStyle().Background(theme.TextNormal).Foreground(theme.TextInk).Render(string(firstRune))
			// 渲染剩余字符(原样式)
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

// renderPermissionBadge renders the current session mode as a badge in the
// input header. Maps the openacp mode id ("auto"|"manual"|"plan") to a color:
// auto=Success(全自动), manual=Primary(需审批), plan=Warning(先出计划).
func (m *Model) renderPermissionBadge() string {
	var col color.Color
	switch m.mode {
	case "auto":
		col = theme.Warning // 黄
	case "plan":
		col = theme.Primary // 蓝
	default: // manual (and any unknown falls back to manual styling)
		col = theme.Success // 绿
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
	// 定义滚动条样式
	thumbStyle := theme.BaseStyle().Background(theme.ThumbBackGround) // 滑块
	trackStyle := theme.BaseStyle().Background(theme.TrackBackGround) // 轨道

	// 预渲染各行
	emptyLine := trackStyle.Render(" ") // 空轨道行
	trackLine := trackStyle.Render(" ") // 轨道行
	thumbLine := thumbStyle.Render(" ") // 滑块行

	totalLines := m.chatViewport.TotalLineCount()
	yOffset := m.chatViewport.YOffset

	var doc strings.Builder
	// 如果内容不超过视口高度,显示空轨道
	if totalLines <= height {
		for i := 0; i < height; i++ {
			doc.WriteString(emptyLine) // 空轨道
			if i < height-1 {
				doc.WriteString("\n")
			}
		}
		return doc.String()
	}
	// 渲染滚动条
	thumbH := max(1, height*height/totalLines)
	maxOffset := totalLines - height
	thumbY := yOffset() * (height - thumbH) / maxOffset
	for i := 0; i < height; i++ {
		if i >= thumbY && i < thumbY+thumbH {
			doc.WriteString(thumbLine) // 滑块部分
		} else {
			doc.WriteString(trackLine) // 轨道部分
		}
		if i < height-1 {
			doc.WriteString("\n")
		}
	}
	return doc.String()
}
