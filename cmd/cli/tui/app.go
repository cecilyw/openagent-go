package tui

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/yusheng-g/openagent-go/cmd/cli/config"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/components"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/theme"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/views/chat"
)

// StartInteractiveTUI launches the fullscreen interactive TUI. It is
// render-only for now: no ACP backend, no message stream — the welcome page
// is shown until a session is created (deferred). ver is shown in the
// footer/sidebar; name is reserved for future backend wiring.
//
// tuiCfg carries the settings.json "tui" section: theme color overrides,
// the placeholder suggestion list, and the initial session mode. Logging is
// configured by the caller (main.go via server.SetupLog) so the TUI never
// writes to stderr and corrupts the alt-screen.
func StartInteractiveTUI(ver, name string, tuiCfg config.TUIConfig) error {
	// 1. Apply theme color overrides before any BaseStyle() call.
	theme.ApplyOverrides(tuiColorMap(tuiCfg.Colors))
	// 2. Override the suggestion list before NewModel consumes the first one.
	components.SetSuggestions(tuiCfg.Suggestions)
	// 3. Override the welcome-page logo (empty keeps the built-in art).
	components.SetLogo(tuiCfg.Logo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 设置终端颜色
	if err := os.Setenv("TERM", "xterm-256color"); err != nil {
		fmt.Println("set TERM failed")
	}

	workDir, _ := os.Getwd()

	p := tea.NewProgram(chat.NewModel(ctx, cancel, workDir, ver, name, tuiCfg.Mode, tuiCfg.Colors.LogoColor, tuiCfg.LogoGradient))
	_, err := p.Run()
	return err
}

// tuiColorMap translates config.TUIColors into the flat map shape
// theme.ApplyOverrides expects (snake_case keys → hex strings). Empty
// fields are dropped so ApplyOverrides keeps the built-in default.
func tuiColorMap(c config.TUIColors) map[string]string {
	m := map[string]string{}
	if c.BgNormal != "" {
		m["bg_normal"] = c.BgNormal
	}
	if c.BgSecondary != "" {
		m["bg_secondary"] = c.BgSecondary
	}
	if c.BgSurface != "" {
		m["bg_surface"] = c.BgSurface
	}
	if c.Primary != "" {
		m["primary"] = c.Primary
	}
	if c.Success != "" {
		m["success"] = c.Success
	}
	if c.Warning != "" {
		m["warning"] = c.Warning
	}
	if c.Danger != "" {
		m["danger"] = c.Danger
	}
	if c.TextNormal != "" {
		m["text_normal"] = c.TextNormal
	}
	if c.TextAsh != "" {
		m["text_ash"] = c.TextAsh
	}
	if c.BorderGray != "" {
		m["border_gray"] = c.BorderGray
	}
	if c.LogoColor != "" {
		m["logo_color"] = c.LogoColor
	}
	return m
}
