package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/skill"
)

func sidebarModel() *model {
	m := &model{}
	m.st = newStyles(builtinThemes["dark"])
	m.themeName = "dark"
	m.rt.Dir = "/work/project"
	m.rt.Model = "gemini"
	m.rt.Agent = &engine.Agent{Model: "gemini"}
	m.width = 120
	return m
}

func editEntry(path, old, new string, status toolStatus) entry {
	in, _ := json.Marshal(map[string]string{"file_path": path, "old_string": old, "new_string": new})
	return entry{kind: "tool", tool: "edit", status: status, input: in}
}

func TestModifiedFilesAggregates(t *testing.T) {
	m := sidebarModel()
	m.entries = []entry{
		editEntry("/work/project/a.go", "x", "y", toolSuccess),
		editEntry("/work/project/a.go", "p\nq", "r\ns\nt", toolSuccess),
		editEntry("/work/project/b.go", "one", "two", toolFail), // failed: skipped
	}
	files := m.modifiedFiles()
	if len(files) != 1 {
		t.Fatalf("want 1 modified file (b.go failed), got %d: %+v", len(files), files)
	}
	if files[0].path != "a.go" {
		t.Errorf("path = %q, want a.go (cwd-relative)", files[0].path)
	}
	// First edit: +1 -1. Second: +3 -2. Total +4 -3.
	if files[0].adds != 4 || files[0].dels != 3 {
		t.Errorf("counts = +%d -%d, want +4 -3", files[0].adds, files[0].dels)
	}
}

func TestSidebarVisibilityBreakpoint(t *testing.T) {
	m := sidebarModel()
	m.showSidebar = true
	if !m.sidebarVisible() {
		t.Error("wide + toggled on should be visible")
	}
	m.width = 50
	if m.sidebarVisible() {
		t.Error("below the breakpoint the sidebar must hide")
	}
	if got := m.transcriptWidth(); got != 50 {
		t.Errorf("hidden sidebar should give full width, got %d", got)
	}
}

func TestTranscriptWidthShrinksForSidebar(t *testing.T) {
	m := sidebarModel()
	m.showSidebar = true
	if got := m.transcriptWidth(); got != 120-sidebarWidth-sidebarGap {
		t.Errorf("transcriptWidth = %d, want %d", got, 120-sidebarWidth-sidebarGap)
	}
}

func TestSidebarRendersSectionsAndPlaceholders(t *testing.T) {
	m := sidebarModel()
	m.rt.Skills = []skill.Skill{{Name: "format"}, {Name: "lint"}}
	m.rt.MCPFailures = map[string]error{"badserver": errors.New("connect refused")}
	m.entries = []entry{editEntry("/work/project/a.go", "x", "y", toolSuccess)}

	out := m.sidebar(30)
	for _, want := range []string{"Model", "Modified Files", "MCP", "Skills", "a.go", "format", "badserver"} {
		if !strings.Contains(out, want) {
			t.Errorf("sidebar missing %q:\n%s", want, out)
		}
	}
	// An empty section shows a placeholder.
	m2 := sidebarModel()
	if !strings.Contains(m2.sidebar(30), "None") {
		t.Error("empty sections should show a None placeholder")
	}
}

func TestSidebarSectionOverflow(t *testing.T) {
	m := sidebarModel()
	rows := make([]string, sectionCap+3)
	for i := range rows {
		rows[i] = "row"
	}
	out := m.sidebarSection("Many", rows)
	if !strings.Contains(out, "and 3 more") {
		t.Errorf("overflow note missing:\n%s", out)
	}
}
