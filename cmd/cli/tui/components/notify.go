package components

import (
	"charm.land/lipgloss/v2"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/theme"
)

type Notify struct {
	content string
}

func NewNotify(content string) Notify {
	return Notify{
		content: content,
	}
}

func (n *Notify) View() string {
	boxStyle := theme.BaseStyle().
		Padding(1).
		Border(lipgloss.ThickBorder(), false, true, false, true).
		BorderForeground(lipgloss.Color("#57b6c3")).
		Foreground(theme.TextNormal)
	return boxStyle.Render(n.content)
}

func (n *Notify) SetContent(content string) string {
	n.content = content
	return content
}
