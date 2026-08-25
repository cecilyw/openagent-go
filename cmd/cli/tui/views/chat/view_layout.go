package chat

import "github.com/yusheng-g/openagent-go/cmd/cli/tui/layout"

func (m *Model) getContentWidth() int {
	if m.activeSessionID == "" {
		return layout.GetWelcomeWidth(m.width)
	}
	return layout.GetContentWidth(m.width)
}
