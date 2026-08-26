package tui

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	openacp "github.com/yusheng-g/openagent-go/acp/sdk"
	"github.com/yusheng-g/openagent-go/cmd/cli/config"
	"github.com/yusheng-g/openagent-go/cmd/cli/server"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/components"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/theme"
	"github.com/yusheng-g/openagent-go/cmd/cli/tui/views/chat"
)

// StartInteractiveTUI launches the fullscreen interactive TUI. It runs the
// ACP server in-process via os.Pipe (no subprocess), connects as an ACP
// client, and streams agent responses into the chat transcript.
//
// cfg provides the full settings (models, memory, capabilities) needed to
// build the ACP server. Logging is configured by the caller (main.go via
// server.SetupLog) so the TUI never writes to stderr.
func StartInteractiveTUI(ver, name string, cfg config.Config, tuiCfg config.TUIConfig) error {
	// 1. Apply theme color overrides before any BaseStyle() call.
	theme.ApplyOverrides(tuiColorMap(tuiCfg.Colors))
	// 2. Override the suggestion list before NewModel consumes the first one.
	components.SetSuggestions(tuiCfg.Suggestions)
	// 3. Override the welcome-page logo (empty keeps the built-in art).
	components.SetLogo(tuiCfg.Logo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := os.Setenv("TERM", "xterm-256color"); err != nil {
		fmt.Println("set TERM failed")
	}

	workDir, _ := os.Getwd()

	// Create model, then program, then inject program back into model so
	// goroutines (ACP handler, Prompt) can send tea.Msg via Program.Send.
	model := chat.NewModel(ctx, cancel, workDir, ver, name, tuiCfg.Mode, tuiCfg.Colors.LogoColor, tuiCfg.LogoGradient)
	p := tea.NewProgram(model)
	model.SetProgram(p)

	// Async: start ACP server in-process via io.Pipe, connect as client.
	caps := cfg.Capabilities
	go startACPInProcess(ctx, model, p, cfg, caps, ver, workDir)

	_, err := p.Run()
	return err
}

// startACPInProcess creates two io.Pipe pairs: one for client→server
// (client writes, server reads) and one for server→client (server writes,
// client reads). The ACP server runs in a goroutine via RunTransport; the
// client connects via ConnectIO. Then it performs the initialize/newSession
// handshake, registers the event handler, and injects the session.
func startACPInProcess(ctx context.Context, model *chat.Model, p *tea.Program, cfg config.Config, caps config.Capabilities, ver, workDir string) {
	// os.Pipe (buffered, 64KB) lets the client write requests before the
	// server finishes building; they buffer until RunTransport reads.
	serverR, clientW, err := os.Pipe()
	if err != nil {
		p.Send(chat.AcpErrorMsg(err))
		return
	}
	clientR, serverW, err := os.Pipe()
	if err != nil {
		p.Send(chat.AcpErrorMsg(err))
		return
	}

	// ACP server: build + run in a goroutine.
	go func() {
		if err := server.RunACPTransport(ctx, &cfg, caps, serverW, serverR); err != nil {
			p.Send(chat.AcpErrorMsg(err))
		}
		_ = serverW.Close()
		_ = serverR.Close()
	}()

	// ACP client: connect and handshake.
	client := openacp.NewClient("openagent-tui", ver)
	sess := client.ConnectIO(ctx, clientW, clientR)

	if _, err := sess.Initialize(ctx, openacp.InitializeRequest{
		ProtocolVersion: 1,
		ClientInfo:      &openacp.Implementation{Name: "openagent-tui", Version: ver},
	}); err != nil {
		p.Send(chat.AcpErrorMsg(err))
		return
	}

	resp, err := sess.NewSession(ctx, openacp.NewSessionRequest{Cwd: workDir})
	if err != nil {
		p.Send(chat.AcpErrorMsg(err))
		return
	}

	sess.SetEventHandler(chat.NewAcpEventHandler(p))
	model.SetACPSession(sess)
	p.Send(chat.AcpReadyMsg(string(resp.SessionID)))
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
