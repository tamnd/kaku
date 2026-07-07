package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstructionsWalkUpAndPrecedence(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	// KAKU.md beats AGENTS.md in the same directory.
	write(t, filepath.Join(sub, "KAKU.md"), "near kaku")
	write(t, filepath.Join(sub, "AGENTS.md"), "should be shadowed")
	write(t, filepath.Join(root, "a", "CLAUDE.md"), "mid claude")
	write(t, filepath.Join(sub, ".kaku", "memory", "fact.md"), "one fact")

	got := Instructions(sub)
	for _, want := range []string{"near kaku", "mid claude", "one fact"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output", want)
		}
	}
	if strings.Contains(got, "shadowed") {
		t.Error("AGENTS.md should be shadowed by KAKU.md")
	}
	if strings.Index(got, "near kaku") > strings.Index(got, "mid claude") {
		t.Error("nearest file should come first")
	}
}

func TestInstructionsEmpty(t *testing.T) {
	if got := Instructions(t.TempDir()); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestInstructionsExtraGlobs(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "CONTRIBUTING.md"), "contrib rules")
	write(t, filepath.Join(root, "docs", "one.md"), "doc one")
	write(t, filepath.Join(root, "docs", "two.md"), "doc two")
	write(t, filepath.Join(root, "docs", "skip.txt"), "not markdown but matched by *.md? no")

	got := Instructions(root, "CONTRIBUTING.md", "docs/*.md")
	for _, want := range []string{"contrib rules", "doc one", "doc two"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output", want)
		}
	}
	if strings.Contains(got, "not markdown") {
		t.Error("docs/*.md should not match a .txt file")
	}
}

func TestInstructionsExtraGlobAbsolute(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "rules.md")
	write(t, abs, "absolute rules")
	if got := Instructions(t.TempDir(), abs); !strings.Contains(got, "absolute rules") {
		t.Errorf("absolute glob not loaded: %q", got)
	}
}
