package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNewDisabledReturnsNil(t *testing.T) {
	if New("/tmp", false, nil) != nil {
		t.Error("disabled manager should be nil")
	}
}

func TestNewNoServersReturnsNil(t *testing.T) {
	// Disable every builtin: nothing owns an extension, so there is no manager.
	specs := map[string]Spec{}
	for _, b := range builtinServers {
		specs[b.name] = Spec{Disabled: true}
	}
	if New("/tmp", true, specs) != nil {
		t.Error("manager with no servers should be nil")
	}
}

func TestUnmatchedExtensionSkips(t *testing.T) {
	m := New(t.TempDir(), true, nil)
	if m == nil {
		t.Fatal("expected a manager with the builtin servers")
	}
	defer m.Close()
	if ds := m.Diagnostics(context.Background(), "notes.txt"); ds != nil {
		t.Errorf("a .txt file has no server, got %v", ds)
	}
}

func TestCustomServerRegistered(t *testing.T) {
	m := New(t.TempDir(), true, map[string]Spec{
		"fake": {Command: []string{"fake-langserver"}, Extensions: []string{".fake"}},
	})
	if m == nil {
		t.Fatal("custom server should produce a manager")
	}
	defer m.Close()
	// The binary does not exist, so ensure caches a nil and Diagnostics skips.
	if ds := m.Diagnostics(context.Background(), "x.fake"); ds != nil {
		t.Errorf("missing binary should skip, got %v", ds)
	}
}

// TestGoplsReportsError drives the real handshake against gopls when it is on
// PATH, proving the manager collects a diagnostic for a broken file.
func TestGoplsReportsError(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/lsptest\n\ngo 1.21\n")
	// notAFunc is undefined, so gopls must flag the call.
	src := filepath.Join(dir, "main.go")
	writeFile(t, src, "package main\n\nfunc main() {\n\tnotAFunc()\n}\n")

	m := New(dir, true, nil)
	if m == nil {
		t.Fatal("expected a manager")
	}
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// gopls can take a moment to warm up; give it a couple of tries.
	var ds []Diagnostic
	for i := 0; i < 3 && len(ds) == 0; i++ {
		ds = m.Diagnostics(ctx, src)
	}
	if len(ds) == 0 {
		t.Fatal("gopls reported no diagnostics for a file that calls an undefined function")
	}
	if ds[0].Severity != "error" {
		t.Errorf("severity = %q, want error", ds[0].Severity)
	}
	if r := m.Report(ctx, src); r == "" {
		t.Error("Report returned empty for a file with an error")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
