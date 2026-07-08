package tui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/perm"
	"github.com/tamnd/kaku/pkg/provider/openai"
	"github.com/tamnd/kaku/pkg/tool"
	"github.com/tamnd/kaku/pkg/tool/builtin"
)

// TestRealModelTranscript drives a live model through the engine and feeds its
// events into the model, then asserts the rich transcript renders markdown and a
// tool call with a status glyph. It is skipped unless a real key is present.
//
// It uses GEMINI_API_KEY against the OpenAI-compatible endpoint. The zen key in
// OPENCODE_API_KEY has no balance at the time of writing, so gemini stands in as
// the real model.
func TestRealModelTranscript(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set; skipping real-model transcript test")
	}
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/hello.txt", []byte("kaku was here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prov := openai.New(key, "https://generativelanguage.googleapis.com/v1beta/openai", "gemini")
	reg := tool.NewRegistry(builtin.All(dir, nil, nil)...)
	agent := &engine.Agent{
		Provider: prov,
		Model:    "gemini-2.5-flash-lite",
		Tools:    reg,
		Perm:     &perm.Engine{Mode: perm.ModeAuto, ReadOnly: reg.ReadOnly},
	}

	m := newModel(context.Background(), Runtime{Agent: agent, Model: "gemini-2.5-flash-lite", Mode: "auto", Dir: dir})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// Apply events synchronously so the rendered transcript reflects the run.
	agent.OnEvent = func(e engine.Event) { m.applyEvent(engine.Event(e)) }

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, err := agent.RunWith(ctx,
		"Read the file hello.txt with the read tool, then reply in markdown with a bulleted list summarizing its contents.", nil)
	if err != nil {
		t.Fatalf("agent run failed: %v", err)
	}
	m.closeThinking()
	m.closeAssistant()
	m.refresh()
	out := m.View()

	// A tool ran, so a tool entry with a status glyph must be present.
	if !containsTool(m.entries) {
		t.Fatalf("expected a tool entry in transcript, got kinds %v", entryKinds(m.entries))
	}
	if !strings.Contains(out, glyphSuccess) && !strings.Contains(out, glyphFail) {
		t.Errorf("expected a tool status glyph in the view:\n%s", out)
	}
	// The assistant turn should have produced some rendered text.
	if !hasAssistantText(m.entries) {
		t.Errorf("expected assistant text in transcript:\n%s", out)
	}
	t.Logf("rendered transcript:\n%s", out)
}

func containsTool(es []entry) bool {
	for _, e := range es {
		if e.kind == "tool" {
			return true
		}
	}
	return false
}

func hasAssistantText(es []entry) bool {
	for _, e := range es {
		if e.kind == "assistant" && strings.TrimSpace(e.text) != "" {
			return true
		}
	}
	return false
}

func entryKinds(es []entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.kind
	}
	return out
}
