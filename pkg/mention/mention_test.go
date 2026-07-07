package mention

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandInlinesFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)

	out, inlined := Expand(dir, "explain @main.go please")
	if !strings.Contains(out, `<file path="main.go">`) || !strings.Contains(out, "package main") {
		t.Fatalf("file not inlined: %q", out)
	}
	if len(inlined) != 1 || inlined[0] != "main.go" {
		t.Fatalf("inlined = %v", inlined)
	}
}

func TestExpandLeavesUnresolved(t *testing.T) {
	dir := t.TempDir()
	out, inlined := Expand(dir, "email me @nobody and see @missing.go")
	if out != "email me @nobody and see @missing.go" {
		t.Fatalf("unresolved tokens should survive: %q", out)
	}
	if len(inlined) != 0 {
		t.Fatalf("nothing should inline: %v", inlined)
	}
}

func TestExpandTrailingDot(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644)
	out, _ := Expand(dir, "see @a.go.")
	if !strings.Contains(out, `<file path="a.go">`) {
		t.Fatalf("trailing dot not trimmed: %q", out)
	}
}

func TestExpandSkipsDir(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	out, inlined := Expand(dir, "look at @sub")
	if out != "look at @sub" || len(inlined) != 0 {
		t.Fatalf("directory should not inline: %q %v", out, inlined)
	}
}

func TestExpandPerFileCap(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, maxPerFile+1)
	os.WriteFile(filepath.Join(dir, "big.txt"), big, 0o644)
	out, inlined := Expand(dir, "@big.txt")
	if out != "@big.txt" || len(inlined) != 0 {
		t.Fatalf("oversized file should be left literal: %q", out)
	}
}
