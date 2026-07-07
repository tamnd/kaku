package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMergesProjectOverUser(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".kaku", "config.json"),
		`{"model":"user-model","permissions":{"allow":["bash(go *)"]}}`)
	write(filepath.Join(dir, ".kaku", "settings.json"),
		`{"model":"proj-model","permissions":{"mode":"auto","allow":["read"]},"mcpServers":{"x":{"command":"srv"}}}`)

	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "proj-model" {
		t.Errorf("model = %q", c.Model)
	}
	if c.Provider != "anthropic" {
		t.Errorf("default provider lost: %q", c.Provider)
	}
	if c.Permissions.Mode != "auto" {
		t.Errorf("mode = %q", c.Permissions.Mode)
	}
	if len(c.Permissions.Allow) != 2 {
		t.Errorf("allow rules should accumulate: %v", c.Permissions.Allow)
	}
	if _, ok := c.MCPServers["x"]; !ok {
		t.Errorf("mcp server missing: %v", c.MCPServers)
	}
}

func TestFormatterConfigUnmarshal(t *testing.T) {
	// A bare bool toggles the builtins.
	var on FormatterConfig
	if err := on.UnmarshalJSON([]byte("true")); err != nil {
		t.Fatal(err)
	}
	if !on.Enabled || on.Specs != nil {
		t.Errorf("true should enable with no specs: %+v", on)
	}
	var off FormatterConfig
	if err := off.UnmarshalJSON([]byte("false")); err != nil {
		t.Fatal(err)
	}
	if off.Enabled {
		t.Error("false should stay disabled")
	}
	// An object enables and carries per-name overrides.
	var obj FormatterConfig
	if err := obj.UnmarshalJSON([]byte(`{"gofmt":{"disabled":true},"deno":{"command":["deno","fmt","$FILE"],"extensions":[".md"]}}`)); err != nil {
		t.Fatal(err)
	}
	if !obj.Enabled {
		t.Error("an object form should enable formatting")
	}
	if !obj.Specs["gofmt"].Disabled {
		t.Error("gofmt should be marked disabled")
	}
	if got := obj.Specs["deno"].Command; len(got) != 3 || got[0] != "deno" {
		t.Errorf("deno command = %v", got)
	}
}

func TestLSPConfigUnmarshal(t *testing.T) {
	var on LSPConfig
	if err := on.UnmarshalJSON([]byte("true")); err != nil {
		t.Fatal(err)
	}
	if !on.Enabled || on.Specs != nil {
		t.Errorf("true should enable with no specs: %+v", on)
	}
	var off LSPConfig
	if err := off.UnmarshalJSON([]byte("false")); err != nil {
		t.Fatal(err)
	}
	if off.Enabled {
		t.Error("false should stay disabled")
	}
	var obj LSPConfig
	if err := obj.UnmarshalJSON([]byte(`{"gopls":{"disabled":true},"zls":{"command":["zls"],"extensions":[".zig"],"language_id":"zig"}}`)); err != nil {
		t.Fatal(err)
	}
	if !obj.Enabled {
		t.Error("an object form should enable diagnostics")
	}
	if !obj.Specs["gopls"].Disabled {
		t.Error("gopls should be marked disabled")
	}
	if got := obj.Specs["zls"]; got.LangID != "zig" || len(got.Command) != 1 {
		t.Errorf("zls spec = %+v", got)
	}
}

func TestLoadMissingFilesIsFine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Model == "" || c.Provider == "" {
		t.Errorf("defaults missing: %+v", c)
	}
}

func TestLoadMalformedIsError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(dir, ".kaku"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".kaku", "settings.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("want error for malformed settings")
	}
}
