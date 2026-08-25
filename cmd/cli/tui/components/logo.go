package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/theme"
)

func GetLogo(widh int) string {
	lines1 := []string{
		"▄▀▀▄  █  ",
		"█▀▀█  █  ",
		"▀  ▀  ▀  ",
	}
	left := strings.Join(lines1, "\n")

	lines2 := []string{
		"▄▀▀▀  █    █  ",
		"█     █    █  ",
		" ▀▀▀  ▀▀▀  ▀  ",
	}
	right := strings.Join(lines2, "\n")
	return lipgloss.JoinHorizontal(lipgloss.Left,
		theme.BaseStyle().Foreground(theme.TextAsh).Align(lipgloss.Right).Render(left),
		theme.BaseStyle().Align(lipgloss.Left).Render(right),
	)
}
