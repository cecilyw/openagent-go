package chat

import (
	"charm.land/lipgloss/v2"

	"github.com/yusheng-g/openagent-go/cmd/cli/tui/components"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/theme"
)

func (m *Model) renderWelcome() string {
	w := m.getContentWidth()
	logoArt := components.GetLogo(w)
	// Apply color: gradient (if set) → single logoColor → default TextAsh.
	colored := components.RenderLogoColored(logoArt, m.logoColor, m.logoGradient)
	if colored == logoArt {
		// no override applied — use theme.LogoColor (defaults to TextAsh)
		colored = theme.BaseStyle().Foreground(theme.LogoColor).Render(logoArt)
	}
	logo := theme.BaseStyle().Width(w).Align(lipgloss.Center).PaddingBottom(1).Render(colored)
	input := m.renderInput()
	status := m.renderStatus()
	content := lipgloss.JoinVertical(lipgloss.Top, logo, input, status, theme.BaseStyle().Width(w).Render(""), theme.BaseStyle().Width(w).Align(lipgloss.Center).Render(m.tips))
	full := lipgloss.Place(m.width, m.height-1, lipgloss.Center, lipgloss.Center, content, lipgloss.WithWhitespaceStyle(theme.BaseStyle()))

	workDirValue := theme.BaseStyle().PaddingLeft(1).Foreground(theme.TextAsh).Render(m.workDir)
	versionValue := theme.BaseStyle().Width(m.width - lipgloss.Width(workDirValue)).PaddingRight(1).Align(lipgloss.Right).Foreground(theme.TextAsh).Render(m.version)
	footer := lipgloss.JoinHorizontal(lipgloss.Left, workDirValue, versionValue)

	return theme.BaseStyle().Width(m.width).Height(m.height).Render(
		lipgloss.JoinVertical(lipgloss.Top,
			full,
			footer,
		),
	)
}
