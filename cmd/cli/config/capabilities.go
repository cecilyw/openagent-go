package config

// Capabilities controls which pluggable modules are enabled, configured
// via settings.json ("capabilities" key). Each field is a *bool: nil means
// "use the default" (some on, some off); explicit true/false in settings
// wins. Command-line flags (--memory=on/off) override settings at startup.
type Capabilities struct {
	Memory     *bool `json:"memory,omitempty"`     // default on
	Summarizer *bool `json:"summarizer,omitempty"` // default on
	Skills     *bool `json:"skills,omitempty"`     // default on
	MCP        *bool `json:"mcp,omitempty"`        // default on
	Guard      *bool `json:"guard,omitempty"`      // default off
	Approver   *bool `json:"approver,omitempty"`   // default off
	Embedder   *bool `json:"embedder,omitempty"`   // default on — opens the knowledge store (CRUD + keyword recall); semantic vector recall requires embedding.* config
}

// on resolves a field against its default.
func (c Capabilities) on(field *bool, defaultOn bool) bool {
	if field != nil {
		return *field
	}
	return defaultOn
}

// OnEmbedder reports whether the knowledge store is enabled (default on).
// The store provides memory CRUD and keyword recall unconditionally; the
// embedding.* config gates semantic vector recall — when no embedding
// provider is configured, the store stays open but vector recall is
// disabled (keyword-only). The built-in BGE embedder was removed; there
// is no embedded model and zero native-lib/cgo dependency.
func (c Capabilities) OnEmbedder() bool { return c.on(c.Embedder, true) }

// OnMemory reports whether Memory is enabled.
func (c Capabilities) OnMemory() bool { return c.on(c.Memory, true) }

// OnSummarizer reports whether Summarizer is enabled.
func (c Capabilities) OnSummarizer() bool { return c.on(c.Summarizer, true) }

// OnSkills reports whether the skill provider is enabled.
func (c Capabilities) OnSkills() bool { return c.on(c.Skills, true) }

// OnMCP reports whether MCP tools are enabled.
func (c Capabilities) OnMCP() bool { return c.on(c.MCP, true) }

// OnGuard reports whether LLM Guard is enabled.
func (c Capabilities) OnGuard() bool { return c.on(c.Guard, false) }

// OnApprover reports whether Approver is enabled.
func (c Capabilities) OnApprover() bool { return c.on(c.Approver, false) }
