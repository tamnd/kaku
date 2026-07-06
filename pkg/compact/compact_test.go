package compact

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tamnd/kaku/pkg/provider"
)

type fakeProvider struct{ lastReq provider.Request }

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Complete(_ context.Context, req provider.Request, _ func(provider.Event)) (*provider.Response, error) {
	f.lastReq = req
	return &provider.Response{Message: provider.Text(provider.RoleAssistant, "THE SUMMARY")}, nil
}

func toolPair(id string) []provider.Message {
	return []provider.Message{
		{Role: provider.RoleAssistant, Content: []provider.Block{
			{Type: provider.BlockToolUse, ID: id, Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
		}},
		{Role: provider.RoleUser, Content: []provider.Block{
			{Type: provider.BlockToolResult, ToolUseID: id, Text: strings.Repeat("x", 400)},
		}},
	}
}

func history() []provider.Message {
	var msgs []provider.Message
	for i := 0; i < 30; i++ {
		msgs = append(msgs, provider.Text(provider.RoleUser, strings.Repeat("q", 200)))
		msgs = append(msgs, toolPair("t")...)
		msgs = append(msgs, provider.Text(provider.RoleAssistant, strings.Repeat("a", 200)))
	}
	return msgs
}

func TestMaybeUnderBudgetNoop(t *testing.T) {
	c := &Compactor{Provider: &fakeProvider{}, Budget: 1 << 30, Keep: 4}
	msgs := history()
	out, changed, err := c.Maybe(context.Background(), msgs)
	if err != nil || changed || len(out) != len(msgs) {
		t.Fatalf("want noop, got changed=%v err=%v", changed, err)
	}
}

func TestMaybeCompacts(t *testing.T) {
	fp := &fakeProvider{}
	c := &Compactor{Provider: fp, Budget: 100, Keep: 6}
	msgs := history()
	out, changed, err := c.Maybe(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected compaction")
	}
	if !strings.Contains(out[0].TextContent(), "THE SUMMARY") {
		t.Fatalf("first message should be the summary: %q", out[0].TextContent())
	}
	if len(out) >= len(msgs) {
		t.Fatalf("history did not shrink: %d -> %d", len(msgs), len(out))
	}
	// The kept tail must not start with an orphan tool_result.
	for _, b := range out[1].Content {
		if b.Type == provider.BlockToolResult {
			t.Fatal("tool_result pairing torn at the boundary")
		}
	}
	if fp.lastReq.MaxTokens != 2000 {
		t.Fatalf("summarize MaxTokens = %d", fp.lastReq.MaxTokens)
	}
}

func TestMaybeNoSafeBoundary(t *testing.T) {
	// Only tool_result user messages: no safe cut point.
	msgs := toolPair("a")
	for i := 0; i < 20; i++ {
		msgs = append(msgs, toolPair("b")...)
	}
	c := &Compactor{Provider: &fakeProvider{}, Budget: 10, Keep: 2}
	_, changed, err := c.Maybe(context.Background(), msgs)
	if err != nil || changed {
		t.Fatalf("want unchanged, got changed=%v err=%v", changed, err)
	}
}

func TestEstimateTokens(t *testing.T) {
	msgs := []provider.Message{provider.Text(provider.RoleUser, strings.Repeat("x", 400))}
	if got := EstimateTokens(msgs); got != 100 {
		t.Fatalf("estimate = %d", got)
	}
}
