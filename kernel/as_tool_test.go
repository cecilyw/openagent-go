package kernel

import (
	"context"
	"encoding/json"
	"testing"

	openagent "github.com/yusheng-g/openagent-go"
	"github.com/yusheng-g/openagent-go/agent"
)

// nameTool is a minimal Tool identified only by its name, for testing
// tool-set composition without exercising real execution.
type nameTool string

func (n nameTool) Definition() openagent.FunctionDefinition {
	return openagent.FunctionDefinition{Name: string(n)}
}

func (n nameTool) Execute(_ context.Context, _ json.RawMessage) *openagent.ToolResult {
	return &openagent.ToolResult{Content: "ok"}
}

// TestNew_RegistersSubAgentAsTool verifies that a configured SubAgent appears
// in the runtime's tool snapshot under its Name, so the model can call it.
// It also confirms sub_agent_send is registered alongside it for follow-ups.
func TestNew_RegistersSubAgentAsTool(t *testing.T) {
	cfg := agent.New("test")
	cfg.SubAgents = []agent.SubAgent{{
		Name:         "explore",
		Description:  "read-only exploration",
		SystemPrompt: "you are explore",
		Tools:        []string{"read", "ls", "grep"},
		MaxTurns:     100,
	}}
	rt := New(cfg, Deps{
		Tools: []openagent.Tool{nameTool("read")},
	})

	names := toolNamesFromSnapshot(rt)
	if !contains(names, "explore") {
		t.Fatalf("explore sub-agent not registered as a tool; got %v", names)
	}
	if !contains(names, "sub_agent_send") {
		t.Errorf("sub_agent_send should be registered alongside explore; got %v", names)
	}
}

// TestNew_NoSubAgentsOmitsSendTool verifies sub_agent_send is NOT registered
// when there are no sub-agents (no delegation targets → no follow-up surface).
func TestNew_NoSubAgentsOmitsSendTool(t *testing.T) {
	cfg := agent.New("test")
	rt := New(cfg, Deps{Tools: []openagent.Tool{nameTool("read")}})

	names := toolNamesFromSnapshot(rt)
	if contains(names, "sub_agent_send") {
		t.Errorf("sub_agent_send should not be registered without sub-agents; got %v", names)
	}
}

// TestFilterChildTools_ExploreReadOnly verifies the explore sub-agent's
// inherited tool set: the allowlist keeps read/ls/grep/shell/websearch/webfetch
// + one-shot browser tools, drops write/edit (mutating) and browser_use_*
// (persistent automation), and always drops sub-agent tools (no recursion).
func TestFilterChildTools_ExploreReadOnly(t *testing.T) {
	parent := []openagent.Tool{
		nameTool("read"), nameTool("write"), nameTool("edit"),
		nameTool("ls"), nameTool("grep"), nameTool("shell"),
		nameTool("websearch"), nameTool("webfetch"),
		nameTool("browser_navigate"), nameTool("browser_screenshot"),
		nameTool("browser_evaluate"), nameTool("browser_click"),
		nameTool("browser_use_open"), nameTool("browser_use_click"),
		nameTool("office_excel"),
		nameTool("explore"), // a sub-agent tool in the parent set
	}
	subAgentNames := map[string]bool{"explore": true}
	allow := []string{
		"read", "ls", "grep", "shell",
		"websearch", "webfetch",
		"browser_navigate", "browser_screenshot", "browser_evaluate", "browser_click",
	}

	got := filterChildTools(parent, subAgentNames, nil, allow)

	names := toolNamesFromFilter(got)
	for _, want := range allow {
		if !contains(names, want) {
			t.Errorf("explore child should keep %q; got %v", want, names)
		}
	}
	for _, blocked := range []string{"write", "edit", "browser_use_open", "browser_use_click", "office_excel", "explore"} {
		if contains(names, blocked) {
			t.Errorf("explore child must not contain %q (read-only + no recursion); got %v", blocked, names)
		}
	}
}

// TestFilterChildTools_NilAllowlistInheritsAllButSubAgents verifies that a
// sub-agent with no tool allowlist inherits every parent tool except other
// sub-agent tools.
func TestFilterChildTools_NilAllowlistInheritsAllButSubAgents(t *testing.T) {
	parent := []openagent.Tool{
		nameTool("read"), nameTool("write"), nameTool("general"),
	}
	subAgentNames := map[string]bool{"general": true}

	got := filterChildTools(parent, subAgentNames, nil, nil)

	names := toolNamesFromFilter(got)
	if contains(names, "general") {
		t.Errorf("sub-agent must not see another sub-agent tool (no recursion); got %v", names)
	}
	if !contains(names, "read") || !contains(names, "write") {
		t.Errorf("nil allowlist should inherit read+write; got %v", names)
	}
}

// --- helpers ---

func toolNamesFromSnapshot(rt *Runtime) []string {
	tools := rt.SnapshotTools()
	out := make([]string, 0, len(tools))
	for _, tl := range tools {
		out = append(out, tl.Definition().Name)
	}
	return out
}

// toolNamesFromFilter extracts names from a filterChildTools result. The
// helper exists because filterChildTools returns []openagent.Tool.
func toolNamesFromFilter(tools []openagent.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, tl := range tools {
		out = append(out, tl.Definition().Name)
	}
	return out
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
