package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tamnd/kaku/pkg/engine"
)

// sized builds a ready model at a fixed terminal size, the way the program has
// one after its first WindowSizeMsg. It is the fixture for View() assertions.
func sized(t *testing.T, rt Runtime) *model {
	t.Helper()
	if rt.Agent == nil {
		rt.Agent = &engine.Agent{}
	}
	m := newModel(context.Background(), rt)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if !m.ready {
		t.Fatal("model not ready after WindowSizeMsg")
	}
	return m
}

func TestViewHelpDialog(t *testing.T) {
	m := sized(t, Runtime{})
	if _, ok := m.slash("/help"); !ok {
		t.Fatal("/help was not handled")
	}
	out := m.View()
	for _, want := range []string{"kaku commands", "/model", "/compact", "esc or enter to dismiss", "╭", "╰"} {
		if !strings.Contains(out, want) {
			t.Errorf("help view missing %q\n%s", want, out)
		}
	}
}

func TestViewModelPicker(t *testing.T) {
	m := sized(t, Runtime{Models: []ModelChoice{
		{Ref: "gemini/gemini-2.5-flash", Label: "gemini/gemini-2.5-flash", Reasoning: "low", Current: true},
		{Ref: "gemini/gemini-2.0-flash", Label: "gemini/gemini-2.0-flash"},
	}})
	if _, ok := m.slash("/model"); !ok {
		t.Fatal("/model was not handled")
	}
	out := m.View()
	for _, want := range []string{"Switch model", "gemini/gemini-2.5-flash", "gemini/gemini-2.0-flash", "current", "↑/↓ move"} {
		if !strings.Contains(out, want) {
			t.Errorf("picker view missing %q\n%s", want, out)
		}
	}
	// The current model row carries the selection caret.
	if !strings.Contains(out, "› gemini/gemini-2.5-flash") {
		t.Errorf("expected caret on the current row\n%s", out)
	}
}

func TestViewErrorDialogFormatsJSON(t *testing.T) {
	m := sized(t, Runtime{})
	m.Update(doneMsg{err: errString(`openai: 404 Not Found: [{ "error": { "message": "models/deepseek is not found" } }]`)})
	out := m.View()
	if !strings.Contains(out, "openai: 404 Not Found") {
		t.Errorf("error view missing clean title\n%s", out)
	}
	if !strings.Contains(out, "models/deepseek is not found") {
		t.Errorf("error view missing extracted message\n%s", out)
	}
	// The raw JSON braces must not leak into the dialog body.
	if strings.Contains(out, `"error":`) {
		t.Errorf("raw json leaked into error view\n%s", out)
	}
}

func TestViewPermissionAsk(t *testing.T) {
	m := sized(t, Runtime{})
	m.ask = &askMsg{tool: "bash", arg: "rm -rf /tmp/x"}
	m.state = stateAsking
	out := m.View()
	for _, want := range []string{"Run bash?", "rm -rf /tmp/x", "[y] once", "[a] always", "[n] deny"} {
		if !strings.Contains(out, want) {
			t.Errorf("ask view missing %q\n%s", want, out)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
