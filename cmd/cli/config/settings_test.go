package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// isolate points the settings file at a temp path so real user settings
// are never touched.
func isolate(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("OPENAGENT_CLI_CONFIG", p)
	return p
}

// UpdateSettings preserves unknown / unrelated fields.
func TestUpdateSettingsPreservesOtherFields(t *testing.T) {
	p := isolate(t)
	existing := map[string]any{
		"provider":            map[string]any{"openai": map[string]any{"api_key": "sk-old"}},
		"unknown_future_field": map[string]any{"keep": "me"},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := UpdateSettings(func(raw map[string]json.RawMessage) error {
		raw["channels"], _ = json.Marshal(map[string]any{"feishu": map[string]string{"app_id": "cli_new"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("settings not valid JSON: %v", err)
	}
	var unknown map[string]string
	if err := json.Unmarshal(got["unknown_future_field"], &unknown); err != nil {
		t.Fatalf("unknown field mangled: %s", got["unknown_future_field"])
	}
	if unknown["keep"] != "me" {
		t.Fatalf("unknown field content lost: %+v", unknown)
	}
	if !json.Valid(got["provider"]) {
		t.Fatalf("provider mangled: %s", got["provider"])
	}
	if string(got["channels"]) == "" {
		t.Fatal("channels not written")
	}
}

// A failing fn aborts the update (nothing written).
func TestUpdateSettingsAbortsOnError(t *testing.T) {
	p := isolate(t)
	if err := UpdateSettings(func(raw map[string]json.RawMessage) error {
		raw["channels"], _ = json.Marshal("x")
		return os.ErrInvalid
	}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("settings file created despite aborted update: %v", err)
	}
}

// Concurrent updates must not lose each other's fields (serialized
// read-modify-write) and must leave valid JSON.
func TestUpdateSettingsConcurrent(t *testing.T) {
	p := isolate(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "field" + string(rune('a'+i))
			if err := UpdateSettings(func(raw map[string]json.RawMessage) error {
				raw[key], _ = json.Marshal(i)
				return nil
			}); err != nil {
				t.Errorf("update %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("settings corrupt after concurrent updates: %v", err)
	}
	// All eight fields landed (none lost to a read-modify-write race).
	for i := 0; i < 8; i++ {
		key := "field" + string(rune('a'+i))
		if _, ok := got[key]; !ok {
			t.Fatalf("field %s lost in concurrent updates", key)
		}
	}
}

// TUI config round-trips through settings.json: the "tui" section parses
// into TUIConfig with the expected fields.
func TestTUIConfigParse(t *testing.T) {
	p := isolate(t)
	settings := `{
		"tui": {
			"mode": "auto",
			"suggestions": ["one", "two"],
			"colors": { "primary": "#ff00ff", "success": "#00ff00" }
		}
	}`
	if err := os.WriteFile(p, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TUI.Mode != "auto" {
		t.Fatalf("mode: want auto, got %q", cfg.TUI.Mode)
	}
	if len(cfg.TUI.Suggestions) != 2 || cfg.TUI.Suggestions[0] != "one" {
		t.Fatalf("suggestions: %v", cfg.TUI.Suggestions)
	}
	if cfg.TUI.Colors.Primary != "#ff00ff" {
		t.Fatalf("primary: %q", cfg.TUI.Colors.Primary)
	}
	if cfg.TUI.Colors.Success != "#00ff00" {
		t.Fatalf("success: %q", cfg.TUI.Colors.Success)
	}
}

// Mode fallback chain: tui.mode → default_mode → "manual". ApplyDefaults
// resolves it so the TUI always ends with a non-empty mode.
func TestTUIConfigModeFallback(t *testing.T) {
	// 1. neither set → manual
	cfg := &Config{}
	ApplyDefaults(cfg, isolate(t))
	if cfg.TUI.Mode != "manual" {
		t.Fatalf("empty fallback: want manual, got %q", cfg.TUI.Mode)
	}
	// 2. default_mode set, tui.mode empty → inherits default_mode
	cfg = &Config{DefaultMode: "plan"}
	ApplyDefaults(cfg, isolate(t))
	if cfg.TUI.Mode != "plan" {
		t.Fatalf("default_mode fallback: want plan, got %q", cfg.TUI.Mode)
	}
	// 3. tui.mode set → wins over default_mode
	cfg = &Config{DefaultMode: "plan", TUI: TUIConfig{Mode: "auto"}}
	ApplyDefaults(cfg, isolate(t))
	if cfg.TUI.Mode != "auto" {
		t.Fatalf("tui.mode precedence: want auto, got %q", cfg.TUI.Mode)
	}
}
