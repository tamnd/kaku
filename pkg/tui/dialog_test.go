package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tamnd/kaku/pkg/engine"
)

func TestPickerNavigateAndSelect(t *testing.T) {
	var picked string
	m := &model{
		rt: Runtime{
			Agent:  &engine.Agent{Model: "start"},
			Models: []ModelChoice{{Ref: "a/one", Label: "a/one"}, {Ref: "b/two", Label: "b/two"}},
		},
	}
	// SwitchModel normally rebuilds the provider and sets Agent.Model; emulate.
	m.rt.SwitchModel = func(ref string) error {
		picked = ref
		m.rt.Agent.Model = ref
		return nil
	}
	m.openModelPicker()
	// Move down to the second row, then select it.
	if _, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}); m.dialog == nil {
		t.Fatal("dialog closed too early")
	}
	if m.dialog.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 after one down", m.dialog.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog != nil {
		t.Fatal("dialog should close after selection")
	}
	if picked != "b/two" {
		t.Errorf("picked = %q, want b/two", picked)
	}
	if m.rt.Model != "b/two" {
		t.Errorf("rt.Model = %q, want b/two", m.rt.Model)
	}
}

func TestErrorDialogDismiss(t *testing.T) {
	m := &model{rt: Runtime{Agent: &engine.Agent{}}}
	m.showError("Boom", "it broke")
	if m.dialog == nil {
		t.Fatal("expected dialog")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog != nil {
		t.Error("esc should dismiss the error dialog")
	}
}

func TestCleanError(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantTitle string
		wantBody  string
	}{
		{
			name:      "openai array error",
			in:        `openai: 404 Not Found: [{ "error": { "code": 404, "message": "models/deepseek is not found for API version v1main", "status": "NOT_FOUND" } }]`,
			wantTitle: "openai: 404 Not Found",
			wantBody:  "models/deepseek is not found for API version v1main",
		},
		{
			name:      "object error",
			in:        `anthropic: 401 Unauthorized: {"error":{"message":"invalid x-api-key"}}`,
			wantTitle: "anthropic: 401 Unauthorized",
			wantBody:  "invalid x-api-key",
		},
		{
			name:      "top level message",
			in:        `provider: {"message":"rate limited"}`,
			wantTitle: "provider",
			wantBody:  "rate limited",
		},
		{
			name:      "no json falls through",
			in:        "connection refused",
			wantTitle: "Error",
			wantBody:  "connection refused",
		},
		{
			name:      "unparseable json keeps raw",
			in:        "boom: {not json",
			wantTitle: "Error",
			wantBody:  "boom: {not json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, body := cleanError(errors.New(tt.in))
			if title != tt.wantTitle {
				t.Errorf("title = %q, want %q", title, tt.wantTitle)
			}
			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

func TestSwitchModelNoRuntimeFunc(t *testing.T) {
	m := &model{rt: Runtime{Model: "old"}}
	m.rt.Agent = nil
	// With no SwitchModel and no Agent we cannot exercise the fallback safely;
	// instead verify the error path formats through showError.
	m.rt.SwitchModel = func(string) error {
		return errors.New(`openai: 400 Bad Request: {"error":{"message":"bad model"}}`)
	}
	m.switchModel("nope")
	if m.dialog == nil || m.dialog.kind != dlgError {
		t.Fatalf("expected an error dialog, got %+v", m.dialog)
	}
	if got := m.dialog.title; got != "Could not switch to nope" {
		t.Errorf("title = %q", got)
	}
}

func TestOpenModelPickerCursorOnCurrent(t *testing.T) {
	m := &model{rt: Runtime{Models: []ModelChoice{
		{Ref: "a/one", Label: "a/one"},
		{Ref: "a/two", Label: "a/two", Current: true, Reasoning: "low"},
	}}}
	m.openModelPicker()
	if m.dialog == nil || m.dialog.kind != dlgPicker {
		t.Fatalf("expected a picker dialog, got %+v", m.dialog)
	}
	if m.dialog.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (the current model)", m.dialog.cursor)
	}
	if got := m.dialog.items[1].desc; got != "low · current" {
		t.Errorf("current desc = %q, want %q", got, "low · current")
	}
}

func TestOpenModelPickerEmpty(t *testing.T) {
	m := &model{rt: Runtime{}}
	m.openModelPicker()
	if m.dialog != nil {
		t.Fatalf("no models should not open a dialog, got %+v", m.dialog)
	}
	if len(m.entries) != 1 || m.entries[0].kind != "info" {
		t.Fatalf("expected one info entry, got %+v", m.entries)
	}
}
