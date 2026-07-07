package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/provider"
)

// decode parses the emitted JSONL into a slice of generic maps.
func decode(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad JSON line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestJSONEmitterStream(t *testing.T) {
	var buf bytes.Buffer
	em := jsonEmitter{w: &buf}

	em.session("sess-1", "big-pickle", "/work")
	em.event(engine.Event{Type: "text", Text: "hello"})
	em.event(engine.Event{Type: "thinking", Text: "hmm"})
	em.event(engine.Event{Type: "tool_start", Tool: "edit", ToolInput: json.RawMessage(`{"path":"a.go"}`)})
	em.event(engine.Event{Type: "tool_end", Tool: "edit", ToolOutput: "ok", IsError: false})
	em.event(engine.Event{Type: "turn", Usage: &provider.Usage{InputTokens: 12, OutputTokens: 3}})
	em.result("final", provider.Usage{InputTokens: 12, OutputTokens: 3})

	lines := decode(t, buf.Bytes())
	if len(lines) != 7 {
		t.Fatalf("want 7 lines, got %d", len(lines))
	}
	if lines[0]["type"] != "session" || lines[0]["id"] != "sess-1" || lines[0]["model"] != "big-pickle" || lines[0]["cwd"] != "/work" {
		t.Fatalf("session header wrong: %v", lines[0])
	}
	if lines[3]["type"] != "tool_start" || lines[3]["tool"] != "edit" {
		t.Fatalf("tool_start wrong: %v", lines[3])
	}
	// input is passed through as a nested object, not a string.
	if in, ok := lines[3]["input"].(map[string]any); !ok || in["path"] != "a.go" {
		t.Fatalf("tool input not inlined as object: %v", lines[3]["input"])
	}
	if lines[5]["type"] != "turn" || lines[5]["input_tokens"].(float64) != 12 {
		t.Fatalf("turn wrong: %v", lines[5])
	}
	last := lines[6]
	if last["type"] != "result" || last["text"] != "final" || last["output_tokens"].(float64) != 3 {
		t.Fatalf("result wrong: %v", last)
	}
}

func TestJSONEmitterError(t *testing.T) {
	var buf bytes.Buffer
	em := jsonEmitter{w: &buf}
	em.session("s", "m", "/x")
	em.fail("boom")
	lines := decode(t, buf.Bytes())
	last := lines[len(lines)-1]
	if last["type"] != "error" || last["error"] != "boom" {
		t.Fatalf("error line wrong: %v", last)
	}
}
