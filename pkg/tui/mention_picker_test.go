package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/tamnd/kaku/pkg/engine"
)

func TestActiveMention(t *testing.T) {
	cases := map[string]string{
		"@ma":       "@ma",
		"foo @bar":  "@bar",
		"a@b":       "", // email-like, not an open mention
		"hi there":  "",
		"@":         "@",
		"look @a/b": "@a/b",
	}
	for in, want := range cases {
		if got := activeMention(in); got != want {
			t.Errorf("activeMention(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRankMentionsBasenamePriority(t *testing.T) {
	files := []string{"pkg/tui/tui.go", "cmd/main.go", "internal/tui.md", "tui.go"}
	got := rankMentions(files, "tui")
	if len(got) == 0 || got[0] != "tui.go" {
		t.Fatalf("ranked = %v, want the shortest basename hit first", got)
	}
	if slices.Contains(got, "cmd/main.go") {
		t.Errorf("main.go has no tui subsequence, should be filtered out: %v", got)
	}
}

func TestRankMentionsEmptySorted(t *testing.T) {
	got := rankMentions([]string{"b.go", "a.go"}, "")
	if got[0] != "a.go" {
		t.Errorf("empty query should sort by path, got %v", got)
	}
}

func TestScanFilesSkipsIgnored(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, "main.go", "package main")
	writeAt(t, dir, "node_modules/dep/index.js", "x")
	writeAt(t, dir, "sub/util.go", "package sub")
	got := scanFiles(dir)
	if !slices.Contains(got, "main.go") || !slices.Contains(got, filepath.Join("sub", "util.go")) {
		t.Errorf("expected main.go and sub/util.go, got %v", got)
	}
	if slices.ContainsFunc(got, func(f string) bool { return filepath.Base(filepath.Dir(f)) == "dep" }) {
		t.Errorf("node_modules should be skipped, got %v", got)
	}
}

func mentionModel(files []string, value string) *model {
	ta := textarea.New()
	ta.SetValue(value)
	m := &model{
		rt:        Runtime{Agent: &engine.Agent{}, Dir: "/x"},
		ta:        ta,
		themes:    LoadThemes(),
		themeName: "dark",
		files:     files,
	}
	m.st = newStyles(builtinThemes["dark"])
	return m
}

func TestUpdateMentionOpensAndCloses(t *testing.T) {
	m := mentionModel([]string{"main.go", "makefile"}, "look at @ma")
	m.updateMention()
	if m.mention == nil || len(m.mention.items) != 2 {
		t.Fatalf("expected the picker open with 2 matches, got %+v", m.mention)
	}
	m.ta.SetValue("look at @main.go ")
	m.updateMention()
	if m.mention != nil {
		t.Errorf("a trailing space closes the picker, got %+v", m.mention)
	}
}

func TestAcceptMentionInsertsPath(t *testing.T) {
	m := mentionModel([]string{"main.go"}, "explain @ma")
	m.updateMention()
	m.acceptMention()
	if got := m.ta.Value(); got != "explain @main.go " {
		t.Errorf("after accept, value = %q, want %q", got, "explain @main.go ")
	}
	if m.mention != nil {
		t.Error("accepting should close the picker")
	}
}

func TestRankMentionsTiers(t *testing.T) {
	// exact basename beats prefix beats path-segment beats a loose subsequence.
	files := []string{
		"pkg/config/loader.go", // "config" is a path segment
		"config.go",            // exact basename
		"configure.go",         // basename prefix
		"cfg/other.go",         // subsequence c-f-g only via "cfg"... not "config"
		"deep/reconfig.go",     // basename contains
	}
	got := rankMentions(files, "config")
	if got[0] != "config.go" {
		t.Fatalf("exact basename should rank first, got %v", got)
	}
	if got[1] != "configure.go" {
		t.Errorf("basename prefix should rank second, got %v", got)
	}
	// "cfg/other.go" has no "config" subsequence, so it drops out.
	if slices.Contains(got, "cfg/other.go") {
		t.Errorf("non-matching file leaked in: %v", got)
	}
}

func TestActiveCommand(t *testing.T) {
	cases := map[string]string{
		"/mod":       "/mod",
		"/":          "/",
		"/model foo": "", // space: a command with an arg, not completing
		"hello":      "",
		"a/b":        "", // a path, not a leading slash
	}
	for in, want := range cases {
		if got := activeCommand(in); got != want {
			t.Errorf("activeCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRankCommandsPrefixFirst(t *testing.T) {
	got := rankCommands("/s")
	if len(got) == 0 {
		t.Fatal("expected /s to match some commands")
	}
	// Every prefix match (/skills, /sessions, /sidebar) sorts before any
	// subsequence-only hit.
	for _, it := range got[:3] {
		if !strings.HasPrefix(it.value, "/s") {
			t.Errorf("prefix matches should lead, got %q in the top three", it.value)
		}
	}
}

func TestUpdateMentionCommandPopup(t *testing.T) {
	m := mentionModel(nil, "/th")
	m.updateMention()
	if m.mention == nil || m.mention.kind != compCommand {
		t.Fatalf("expected a command popup, got %+v", m.mention)
	}
	// /theme and /thinking both match "/th".
	if len(m.mention.items) < 2 {
		t.Errorf("expected at least two /th matches, got %d", len(m.mention.items))
	}
	m.acceptMention()
	if got := m.ta.Value(); !strings.HasPrefix(got, "/th") || !strings.HasSuffix(got, " ") {
		t.Errorf("accepting a command should fill the line and trail a space, got %q", got)
	}
}

func TestMoveMentionWraps(t *testing.T) {
	m := mentionModel([]string{"a.go", "b.go", "c.go"}, "@")
	m.updateMention()
	n := len(m.mention.items)
	m.mention.cursor = 0
	m.moveMention(-1)
	if m.mention.cursor != n-1 {
		t.Errorf("up from the top should wrap to the bottom, got %d", m.mention.cursor)
	}
	m.moveMention(1)
	if m.mention.cursor != 0 {
		t.Errorf("down from the bottom should wrap to the top, got %d", m.mention.cursor)
	}
}

func TestMentionSelectionResetsOnQueryChange(t *testing.T) {
	m := mentionModel([]string{"alpha.go", "alto.go", "album.go"}, "@al")
	m.updateMention()
	m.mention.cursor = 2
	m.ta.SetValue("@alp")
	m.updateMention()
	if m.mention.cursor != 0 {
		t.Errorf("a changed query resets the selection to the top, got %d", m.mention.cursor)
	}
}

func writeAt(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
