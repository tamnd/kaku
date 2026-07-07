package main

import (
	"encoding/json"
	"io"

	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/provider"
)

// wireEvent is the stable JSONL shape a headless --json run emits, one object
// per line. It is kept here in cmd/kaku so the engine stays free of output
// formatting.
type wireEvent struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	Tool         string          `json:"tool,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	Output       string          `json:"output,omitempty"`
	IsError      bool            `json:"is_error,omitempty"`
	InputTokens  int             `json:"input_tokens,omitempty"`
	OutputTokens int             `json:"output_tokens,omitempty"`
	ID           string          `json:"id,omitempty"`
	Model        string          `json:"model,omitempty"`
	Cwd          string          `json:"cwd,omitempty"`
	Error        string          `json:"error,omitempty"`
}

// jsonEmitter writes wire events as JSONL to w, one per line.
type jsonEmitter struct{ w io.Writer }

func (e jsonEmitter) emit(v wireEvent) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	e.w.Write(append(data, '\n'))
}

// event maps an engine event onto the wire shape. It returns false for event
// types that carry nothing a consumer needs (none today, but keeps the switch
// explicit).
func (e jsonEmitter) event(ev engine.Event) {
	switch ev.Type {
	case "text", "thinking", "info":
		e.emit(wireEvent{Type: ev.Type, Text: ev.Text})
	case "tool_start":
		e.emit(wireEvent{Type: "tool_start", Tool: ev.Tool, Input: ev.ToolInput})
	case "tool_end":
		e.emit(wireEvent{Type: "tool_end", Tool: ev.Tool, Output: ev.ToolOutput, IsError: ev.IsError})
	case "turn":
		w := wireEvent{Type: "turn"}
		if ev.Usage != nil {
			w.InputTokens = ev.Usage.InputTokens
			w.OutputTokens = ev.Usage.OutputTokens
		}
		e.emit(w)
	}
}

func (e jsonEmitter) session(id, model, cwd string) {
	e.emit(wireEvent{Type: "session", ID: id, Model: model, Cwd: cwd})
}

func (e jsonEmitter) result(text string, u provider.Usage) {
	e.emit(wireEvent{Type: "result", Text: text, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens})
}

func (e jsonEmitter) fail(msg string) {
	e.emit(wireEvent{Type: "error", Error: msg})
}
