package governance

import (
	"context"
	"encoding/json"
	"testing"

	openagent "github.com/yusheng-g/openagent-go"
)

// fakeHuman records Ask calls and returns a scripted decision.
type fakeHuman struct {
	asked    int
	decision Decision
}

func (f *fakeHuman) Ask(context.Context, openagent.ToolCall, openagent.FunctionDefinition, openagent.Session) (Decision, error) {
	f.asked++
	return f.decision, nil
}

func call(name string) openagent.ToolCall {
	return openagent.ToolCall{
		ID:   "c1",
		Type: "function",
		Function: openagent.ToolCallFunction{
			Name:      name,
			Arguments: "{}",
		},
	}
}

func def(name string) openagent.FunctionDefinition {
	return openagent.FunctionDefinition{Name: name}
}

func callWithArgs(name, args string) openagent.ToolCall {
	c := call(name)
	c.Function.Arguments = args
	return c
}

// TestEngine_RulesLayer: every glob form gates tool calls before the
// safety/human layers. The default engine's handoff rule
// ("transfer_to_*") must actually match — this was broken until the
// prefix-glob fix.
func TestEngine_RulesLayer(t *testing.T) {
	human := &fakeHuman{decision: Decision{Action: Deny, Reason: "user said no"}}
	e := NewEngine([]Rule{
		{ToolPattern: "transfer_to_*", Action: Allow, Reason: "handoff"},
		{ToolPattern: "*_agent", Action: Allow},
		{ToolPattern: "*grep*", Action: Allow},
		{ToolPattern: "shell", Action: Deny, Reason: "no shell"},
		{ToolPattern: "*", Action: Allow}, // catch-all last (first match wins)
	}, NewToolClassifier(), nil, human)

	cases := map[string]ApprovalAction{
		"transfer_to_designer": Allow, // prefix glob
		"build_agent":          Allow, // suffix glob
		"mygrepx":              Allow, // contains glob
		"anything":             Allow, // bare *
		"shell":                Deny,  // earlier exact rule beats the catch-all
	}
	for name, want := range cases {
		d, err := e.Evaluate(context.Background(), call(name), def(name), openagent.Session{})
		if err != nil {
			t.Fatal(err)
		}
		if d.Action != want {
			t.Errorf("%s action = %v, want %v", name, d.Action, want)
		}
	}
	if human.asked != 0 {
		t.Fatalf("human consulted for rule-covered tools (%d asks)", human.asked)
	}
}

// TestEngine_ArgsPattern: a rule with ArgsPattern only matches calls whose
// args contain every key with the same value.
func TestEngine_ArgsPattern(t *testing.T) {
	human := &fakeHuman{decision: Decision{Action: Ask}}
	e := NewEngine([]Rule{{
		ToolPattern: "write",
		ArgsPattern: map[string]any{"path": "/etc/passwd"},
		Action:      Deny,
	}}, nil, nil, human)

	d, err := e.Evaluate(context.Background(), callWithArgs("write", `{"path":"/etc/passwd"}`), def("write"), openagent.Session{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != Deny {
		t.Fatalf("matching args action = %v, want Deny", d.Action)
	}
	if human.asked != 0 {
		t.Fatalf("human consulted for rule-matched call (%d asks)", human.asked)
	}

	d, err = e.Evaluate(context.Background(), callWithArgs("write", `{"path":"/tmp/ok"}`), def("write"), openagent.Session{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != Ask {
		t.Fatalf("non-matching args action = %v, want Ask (defer to human)", d.Action)
	}
}

// TestEngine_RememberedAskRoutesToHuman: a remembered Ask decision must
// still consult the human, never short-circuit into execution.
func TestEngine_RememberedAskRoutesToHuman(t *testing.T) {
	mem := NewSessionApprovalMemory()
	human := &fakeHuman{decision: Decision{Action: Allow}}
	e := NewEngine(nil, nil, mem, human)
	ctx := context.Background()
	sess := openagent.Session{ID: "s1"}

	if err := mem.Remember(ctx, sess.ID, ApprovalKey("shell", json.RawMessage("{}")), Decision{Action: Ask, Reason: "ask always"}); err != nil {
		t.Fatal(err)
	}
	d, err := e.Evaluate(ctx, call("shell"), def("shell"), sess)
	if err != nil {
		t.Fatal(err)
	}
	if human.asked != 1 {
		t.Fatalf("human asks = %d, want 1 (Ask must not short-circuit)", human.asked)
	}
	if d.Action != Allow {
		t.Fatalf("action = %v, want Allow (the human's decision)", d.Action)
	}
}

// TestEngine_ReadOnlyAutoAllowed: platform classification allows read-only
// tools without consulting the human layer (replaces the legacy
// CanSelfApprove self-declaration).
func TestEngine_ReadOnlyAutoAllowed(t *testing.T) {
	human := &fakeHuman{decision: Decision{Action: Deny}}
	e := NewEngine(nil, NewToolClassifier(), nil, human)

	d, err := e.Evaluate(context.Background(), call("read"), def("read"), openagent.Session{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != Allow {
		t.Fatalf("read-only tool action = %v, want Allow", d.Action)
	}
	if human.asked != 0 {
		t.Fatalf("human consulted for read-only tool (%d asks)", human.asked)
	}

	d, _ = e.Evaluate(context.Background(), call("ls"), def("ls"), openagent.Session{})
	if d.Action != Allow {
		t.Fatalf("ls action = %v, want Allow", d.Action)
	}
}

// TestEngine_DangerousConsultsHuman: non-readonly tools reach the human layer.
func TestEngine_DangerousConsultsHuman(t *testing.T) {
	human := &fakeHuman{decision: Decision{Action: Deny, Reason: "user said no"}}
	e := NewEngine(nil, NewToolClassifier(), nil, human)

	d, err := e.Evaluate(context.Background(), call("shell"), def("shell"), openagent.Session{})
	if err != nil {
		t.Fatal(err)
	}
	if human.asked != 1 {
		t.Fatalf("human asks = %d, want 1", human.asked)
	}
	if d.Action != Deny {
		t.Fatalf("action = %v, want Deny", d.Action)
	}
}

// TestEngine_AlwaysRememberedSkipsHuman: after an "always" decision is
// remembered for a tool, subsequent calls short-circuit without asking.
func TestEngine_AlwaysRememberedSkipsHuman(t *testing.T) {
	mem := NewSessionApprovalMemory()
	human := &fakeHuman{decision: Decision{Action: Allow, Reason: "always allow"}}
	e := NewEngine(nil, NewToolClassifier(), mem, human)
	ctx := context.Background()
	sess := openagent.Session{ID: "s1"}

	// First call: human decides; the bridge would Remember on "always" —
	// simulate that (as rest/acp bridges do).
	d1, _ := e.Evaluate(ctx, call("shell"), def("shell"), sess)
	if d1.Action != Allow {
		t.Fatalf("first action = %v", d1.Action)
	}
	// Memory is keyed by tool + canonical args (the bridge computes this).
	_ = mem.Remember(ctx, sess.ID, ApprovalKey("shell", json.RawMessage("{}")), d1)

	// Second call: memory layer short-circuits, human not asked again.
	d2, _ := e.Evaluate(ctx, call("shell"), def("shell"), sess)
	if d2.Action != Allow {
		t.Fatalf("remembered action = %v, want Allow", d2.Action)
	}
	if human.asked != 1 {
		t.Fatalf("human asks = %d, want 1 (memory should have skipped)", human.asked)
	}
}

// ── Multi-key (shell/write) memory semantics ──

// Shell allow-always needs EVERY command atom and file access remembered:
// a new command in a chain re-asks, a reused one doesn't.
func TestEngine_ShellAllKeysMustMatch(t *testing.T) {
	mem := NewSessionApprovalMemory()
	human := &fakeHuman{decision: Decision{Action: Allow, Reason: "always allow"}}
	e := NewEngine(nil, NewToolClassifier(), mem, human)
	ctx := context.Background()
	sess := openagent.Session{ID: "s1"}

	// Remember the atoms of `cat a | grep x` (as the bridge does on
	// allow_always) — but NOT the file access of a redirection variant.
	cmd := `{"command":"cat a | grep x"}`
	keys := MemoryKeys("shell", json.RawMessage(cmd))
	if len(keys) != 2 {
		t.Fatalf("keys = %v, want [cat a, grep x]", keys)
	}
	for _, k := range keys {
		if err := mem.Remember(ctx, sess.ID, k, Decision{Action: Allow, Reason: "always allow"}); err != nil {
			t.Fatal(err)
		}
	}

	// Same chain: all atoms remembered → skips the human.
	d, _ := e.Evaluate(ctx, callWithArgs("shell", cmd), def("shell"), sess)
	if d.Action != Allow {
		t.Fatalf("same chain action = %v, want Allow", d.Action)
	}
	if human.asked != 0 {
		t.Fatalf("human asks = %d, want 0 (all keys remembered)", human.asked)
	}

	// Changed chain member: `cat b | grep x` — cat b not remembered → ask.
	if _, _ = e.Evaluate(ctx, callWithArgs("shell", `{"command":"cat b | grep x"}`), def("shell"), sess); human.asked != 1 {
		t.Fatalf("human asks = %d, want 1 (cat b is new)", human.asked)
	}

	// New file access: `cat a | grep x > /etc/shadow` — write not
	// remembered → ask (sensitive dir keeps single-file granularity).
	if _, _ = e.Evaluate(ctx, callWithArgs("shell", `{"command":"cat a | grep x > /etc/shadow"}`), def("shell"), sess); human.asked != 2 {
		t.Fatalf("human asks = %d, want 2 (write /etc/shadow is new)", human.asked)
	}
}

// write grants are directory-level: approving one file in a directory
// covers the directory's other files (dynamic filenames), and approving
// a read never covers a write.
func TestEngine_WriteDirectoryLevelGrant(t *testing.T) {
	mem := NewSessionApprovalMemory()
	human := &fakeHuman{decision: Decision{Action: Allow, Reason: "always allow"}}
	e := NewEngine(nil, NewToolClassifier(), mem, human)
	ctx := context.Background()
	sess := openagent.Session{ID: "s1"}

	// Approve writing out/result-1.txt — the grant unit is out/.
	k := MemoryKeys("write", json.RawMessage(`{"path":"out/result-1.txt","content":"x"}`))
	if len(k) != 1 {
		t.Fatalf("write keys = %v", k)
	}
	if err := mem.Remember(ctx, sess.ID, k[0], Decision{Action: Allow, Reason: "always allow"}); err != nil {
		t.Fatal(err)
	}

	// Another file in the same directory: covered.
	if d, _ := e.Evaluate(ctx, callWithArgs("write", `{"path":"out/result-2.txt","content":"y"}`), def("write"), sess); d.Action != Allow {
		t.Fatalf("same-dir write action = %v, want Allow", d.Action)
	}
	if human.asked != 0 {
		t.Fatalf("human asks = %d, want 0 (directory-level grant)", human.asked)
	}

	// A different directory: not covered.
	if _, _ = e.Evaluate(ctx, callWithArgs("write", `{"path":"src/result-1.txt","content":"z"}`), def("write"), sess); human.asked != 1 {
		t.Fatalf("human asks = %d, want 1 (src/ not granted)", human.asked)
	}

	// A sensitive path: /etc/passwd grant does NOT cover /etc/shadow.
	keys := MemoryKeys("write", json.RawMessage(`{"path":"/etc/passwd","content":"x"}`))
	if err := mem.Remember(ctx, sess.ID, keys[0], Decision{Action: Allow, Reason: "always allow"}); err != nil {
		t.Fatal(err)
	}
	if _, _ = e.Evaluate(ctx, callWithArgs("write", `{"path":"/etc/shadow","content":"y"}`), def("write"), sess); human.asked != 2 {
		t.Fatalf("human asks = %d, want 2 (sensitive dir keeps single-file)", human.asked)
	}
}
