package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/skill"
)

func landingModel() *model {
	m := &model{}
	m.st = newStyles(builtinThemes["dark"])
	m.themeName = "dark"
	m.rt.Dir = "/work/project"
	m.rt.Model = "gemini"
	m.rt.Mode = "build"
	m.rt.Agent = &engine.Agent{Model: "gemini"}
	m.width = 100
	return m
}

func TestTranscriptEmptyIgnoresInfo(t *testing.T) {
	m := landingModel()
	if !m.transcriptEmpty() {
		t.Fatal("fresh model should be empty")
	}
	m.entries = []entry{{kind: "info", text: "startup"}, {kind: "error", text: "mcp failed"}}
	if !m.transcriptEmpty() {
		t.Error("info and error entries must not clear the landing")
	}
	m.entries = append(m.entries, entry{kind: "user", text: "hello"})
	if m.transcriptEmpty() {
		t.Error("a user turn must clear the landing")
	}
}

func TestLandingShowsContext(t *testing.T) {
	m := landingModel()
	m.rt.Skills = []skill.Skill{{Name: "format"}, {Name: "lint"}}
	m.rt.MCPFailures = map[string]error{"badserver": errors.New("connect refused")}
	m.resumeNote = "resumed session abc123 (4 messages)"

	out := m.landing(100)
	for _, want := range []string{"cwd", "gemini", "Skills", "format", "MCP", "badserver", "resumed session abc123"} {
		if !strings.Contains(out, want) {
			t.Errorf("landing missing %q:\n%s", want, out)
		}
	}
}

func TestLandingNoResumeNoteWhenFresh(t *testing.T) {
	m := landingModel()
	out := m.landing(100)
	if strings.Contains(out, "resumed session") {
		t.Error("a fresh session must not show a resume note")
	}
}
