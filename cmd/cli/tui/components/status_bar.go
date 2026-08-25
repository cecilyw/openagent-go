package components

import (
	"charm.land/lipgloss/v2"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/theme"
)

type StatusBar struct {
	Help   string
	Status string
	Width  int
}

func NewStatusBar() StatusBar {
	return StatusBar{}
}

func (m StatusBar) View() string {
	left := m.Status
	right := m.Help
	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gapW := max(0, m.Width-leftW-rightW)
	gap := theme.BaseStyle().Width(gapW).Render("")
	return theme.BaseStyle().Width(m.Width).Foreground(theme.TextAsh).Render(left + gap + right)
}
