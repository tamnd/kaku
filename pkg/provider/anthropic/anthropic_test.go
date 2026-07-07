package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/kaku/pkg/provider"
)

func sse(w http.ResponseWriter, typ, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, data)
}

func TestCompleteText(t *testing.T) {
	var got apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "k" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("headers = %v", r.Header)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "message_start", `{"type":"message_start","message":{"usage":{"input_tokens":12}}}`)
		sse(w, "content_block_start", `{"type":"content_block_start","content_block":{"type":"text"}}`)
		sse(w, "content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi "}}`)
		sse(w, "content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"there"}}`)
		sse(w, "content_block_stop", `{"type":"content_block_stop"}`)
		sse(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)
		sse(w, "message_stop", `{"type":"message_stop"}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL)
	var deltas strings.Builder
	res, err := c.Complete(context.Background(), provider.Request{
		Model:     "m",
		System:    "sys",
		MaxTokens: 100,
		Messages:  []provider.Message{provider.Text(provider.RoleUser, "hello")},
	}, func(ev provider.Event) {
		if ev.Type == "text" {
			deltas.WriteString(ev.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.System != "sys" || got.Model != "m" || !got.Stream || got.MaxTokens != 100 {
		t.Errorf("request = %+v", got)
	}
	if deltas.String() != "hi there" {
		t.Errorf("deltas = %q", deltas.String())
	}
	if res.Message.TextContent() != "hi there" {
		t.Errorf("text = %q", res.Message.TextContent())
	}
	if res.StopReason != provider.StopEndTurn {
		t.Errorf("stop = %q", res.StopReason)
	}
	if res.Usage.InputTokens != 12 || res.Usage.OutputTokens != 5 {
		t.Errorf("usage = %+v", res.Usage)
	}
}

func TestCompleteToolUse(t *testing.T) {
	var got apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "content_block_start", `{"type":"content_block_start","content_block":{"type":"tool_use","id":"tu_1","name":"bash"}}`)
		sse(w, "content_block_delta", `{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}`)
		sse(w, "content_block_delta", `{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"\"ls\"}"}}`)
		sse(w, "content_block_stop", `{"type":"content_block_stop"}`)
		sse(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`)
		sse(w, "message_stop", `{"type":"message_stop"}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL)
	var started string
	// History with a prior tool round, to exercise the request mapping.
	history := []provider.Message{
		provider.Text(provider.RoleUser, "list files"),
		{Role: provider.RoleAssistant, Content: []provider.Block{
			{Type: provider.BlockToolUse, ID: "tu_0", Name: "bash", Input: json.RawMessage(`{"command":"pwd"}`)},
		}},
		{Role: provider.RoleUser, Content: []provider.Block{
			{Type: provider.BlockToolResult, ToolUseID: "tu_0", Text: "/tmp", IsError: false},
		}},
	}
	res, err := c.Complete(context.Background(), provider.Request{
		Model:    "m",
		Messages: history,
		Tools:    []provider.ToolDef{{Name: "bash", Description: "run", Schema: json.RawMessage(`{"type":"object"}`)}},
	}, func(ev provider.Event) {
		if ev.Type == "tool_start" {
			started = ev.Tool
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "bash" {
		t.Errorf("tools = %+v", got.Tools)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("messages = %+v", got.Messages)
	}
	if b := got.Messages[1].Content[0]; b.Type != "tool_use" || b.ID != "tu_0" {
		t.Errorf("tool_use block = %+v", b)
	}
	if b := got.Messages[2].Content[0]; b.Type != "tool_result" || b.ToolUseID != "tu_0" || b.Content != "/tmp" {
		t.Errorf("tool_result block = %+v", b)
	}
	if started != "bash" {
		t.Errorf("tool_start = %q", started)
	}
	uses := res.Message.ToolUses()
	if len(uses) != 1 || uses[0].ID != "tu_1" || string(uses[0].Input) != `{"command":"ls"}` {
		t.Errorf("tool uses = %+v", uses)
	}
	if res.StopReason != provider.StopToolUse {
		t.Errorf("stop = %q", res.StopReason)
	}
}

func TestCompleteStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "error", `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL)
	_, err := c.Complete(context.Background(), provider.Request{Model: "m"}, nil)
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("err = %v", err)
	}
}

func TestRetryOn429(t *testing.T) {
	old := retryDelays
	retryDelays = []time.Duration{0}
	defer func() { retryDelays = old }()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "content_block_start", `{"type":"content_block_start","content_block":{"type":"text"}}`)
		sse(w, "content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}`)
		sse(w, "content_block_stop", `{"type":"content_block_stop"}`)
		sse(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)
		sse(w, "message_stop", `{"type":"message_stop"}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL)
	res, err := c.Complete(context.Background(), provider.Request{Model: "m"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || res.Message.TextContent() != "ok" {
		t.Errorf("calls = %d, text = %q", calls, res.Message.TextContent())
	}
}

func TestNoRetryOn400(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	c := New("k", srv.URL)
	_, err := c.Complete(context.Background(), provider.Request{Model: "m"}, nil)
	if err == nil || calls != 1 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
}

func TestBuildRequestReasoning(t *testing.T) {
	req := provider.Request{Model: "m", MaxTokens: 1000, Reasoning: "high"}
	out := buildRequest(req)
	if out.Thinking == nil || out.Thinking.Type != "enabled" || out.Thinking.BudgetTokens != 16384 {
		t.Fatalf("thinking not set for high: %+v", out.Thinking)
	}
	// max_tokens must exceed the thinking budget.
	if out.MaxTokens <= out.Thinking.BudgetTokens {
		t.Fatalf("max_tokens %d not above budget %d", out.MaxTokens, out.Thinking.BudgetTokens)
	}

	off := buildRequest(provider.Request{Model: "m", MaxTokens: 1000})
	if off.Thinking != nil {
		t.Fatalf("thinking should be nil when reasoning is empty: %+v", off.Thinking)
	}
	if off.MaxTokens != 1000 {
		t.Fatalf("max_tokens should be unchanged when off: %d", off.MaxTokens)
	}
}

func TestBuildRequestImageBlock(t *testing.T) {
	req := provider.Request{
		Model:     "m",
		MaxTokens: 1000,
		Messages: []provider.Message{{
			Role: provider.RoleUser,
			Content: []provider.Block{
				{Type: provider.BlockText, Text: "what is this"},
				provider.Image("image/png", "AAAA"),
			},
		}},
	}
	out := buildRequest(req)
	var img *apiBlock
	for i, b := range out.Messages[0].Content {
		if b.Type == "image" {
			img = &out.Messages[0].Content[i]
		}
	}
	if img == nil || img.Source == nil {
		t.Fatalf("image block missing a source: %+v", out.Messages[0].Content)
	}
	if img.Source.Type != "base64" || img.Source.MediaType != "image/png" || img.Source.Data != "AAAA" {
		t.Fatalf("image source = %+v", img.Source)
	}
}

func TestBuildRequestOutputSchema(t *testing.T) {
	schema := `{"type":"object","properties":{"n":{"type":"integer"}}}`
	out := buildRequest(provider.Request{
		Model:        "m",
		MaxTokens:    1000,
		System:       "be terse",
		OutputSchema: []byte(schema),
	})
	// The messages API has no format field, so the schema rides in the system
	// prompt, appended after the existing instructions.
	if !strings.Contains(out.System, "be terse") {
		t.Errorf("existing system prompt should survive: %q", out.System)
	}
	if !strings.Contains(out.System, schema) {
		t.Errorf("schema should be folded into the system prompt: %q", out.System)
	}
	// No schema leaves the system prompt untouched.
	plain := buildRequest(provider.Request{Model: "m", MaxTokens: 1000, System: "be terse"})
	if plain.System != "be terse" {
		t.Errorf("system prompt should be unchanged without a schema: %q", plain.System)
	}
}

func TestBuildRequestKeepsThinkingBlock(t *testing.T) {
	req := provider.Request{
		Model:     "m",
		MaxTokens: 1000,
		Reasoning: "medium",
		Messages: []provider.Message{{
			Role: provider.RoleAssistant,
			Content: []provider.Block{
				{Type: provider.BlockThinking, Text: "hmm", Signature: "sig-1"},
				{Type: provider.BlockText, Text: "answer"},
			},
		}},
	}
	out := buildRequest(req)
	var sawThinking bool
	for _, b := range out.Messages[0].Content {
		if b.Type == "thinking" && b.Signature == "sig-1" {
			sawThinking = true
		}
	}
	if !sawThinking {
		t.Fatalf("thinking block with signature not sent back: %+v", out.Messages[0].Content)
	}
	// With thinking off, the block is dropped.
	req.Reasoning = "off"
	off := buildRequest(req)
	for _, b := range off.Messages[0].Content {
		if b.Type == "thinking" {
			t.Fatalf("thinking block should be dropped when off")
		}
	}
}
