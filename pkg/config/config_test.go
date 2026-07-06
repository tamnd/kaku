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
