package chat

import (
	tea "charm.land/bubbletea/v2"

	openacp "github.com/yusheng-g/openagent-go/acp/sdk"
)

// acpEventHandler implements openacp.EventHandler. The ACP SDK calls these
// methods from a background reader goroutine as streaming notifications
// arrive from the agent. Each sends a tea.Msg to the program via
// Program.Send (thread-safe), so all model state changes happen in the
// bubbletea Update loop — no locks needed.
//
// Only agent messages and thoughts are handled for now; tool calls, plans,
// config, and usage updates are no-ops (not yet implemented).
// NewAcpEventHandler creates an EventHandler that forwards agent streaming
// events to the bubbletea program via Program.Send.
func NewAcpEventHandler(p *tea.Program) openacp.EventHandler {
	return &acpEventHandler{program: p}
}

type acpEventHandler struct {
	program *tea.Program
}

func (h *acpEventHandler) OnAgentMessage(text string) {
	h.program.Send(agentMessageMsg{text: text})
}

func (h *acpEventHandler) OnAgentThought(text string) {
	h.program.Send(agentThoughtMsg{text: text})
}

func (h *acpEventHandler) OnUserMessage(text string) {}

func (h *acpEventHandler) OnToolCall(tc openacp.ToolCallUpdate) {}

func (h *acpEventHandler) OnPlan(plan openacp.Plan) {}

func (h *acpEventHandler) OnAvailableCommandsUpdate(cmds []openacp.AvailableCommand) {
}

func (h *acpEventHandler) OnModeUpdate(modeID openacp.SessionModeId) {}

func (h *acpEventHandler) OnConfigOptionUpdate(opts []openacp.SessionConfigOption) {
}

func (h *acpEventHandler) OnUsageUpdate(used, total int, cost *openacp.Cost) {}

func (h *acpEventHandler) OnSessionInfo(title string, metadata map[string]any) {}
