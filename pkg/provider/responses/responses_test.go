package responses

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

func sse(w http.ResponseWriter, typ string, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, data)
}

func TestCompleteText(t *testing.T) {
	var got apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.output_text.delta", `{"type":"response.output_text.delta","delta":"hi "}`)
		sse(w, "response.output_text.delta", `{"type":"response.output_text.delta","delta":"there"}`)
		sse(w, "response.completed", `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi there"}]}],"usage":{"input_tokens":7,"output_tokens":2}}}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL+"/v1", "")
	var deltas strings.Builder
	res, err := c.Complete(context.Background(), provider.Request{
		Model:    "m",
		System:   "sys",
		Messages: []provider.Message{provider.Text(provider.RoleUser, "hello")},
	}, func(ev provider.Event) {
		if ev.Type == "text" {
			deltas.WriteString(ev.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Instructions != "sys" || !got.Stream || got.Store {
		t.Errorf("request = %+v", got)
	}
	if len(got.Input) != 1 || got.Input[0].Type != "message" || got.Input[0].Role != "user" {
		t.Errorf("input = %+v", got.Input)
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
	if res.Usage.InputTokens != 7 || res.Usage.OutputTokens != 2 {
		t.Errorf("usage = %+v", res.Usage)
	}
}

func TestCompleteToolCall(t *testing.T) {
	var got apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.output_item.added", `{"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1","name":"bash","arguments":""}}`)
		sse(w, "response.completed", `{"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"bash","arguments":"{\"command\":\"ls\"}"}],"usage":{"input_tokens":1,"output_tokens":1}}}`)
	}))
	defer srv.Close()

	c := New("", srv.URL+"/v1", "")
	var started string
	// History with a prior tool round, to exercise the input mapping.
	history := []provider.Message{
		provider.Text(provider.RoleUser, "list files"),
		{Role: provider.RoleAssistant, Content: []provider.Block{
			{Type: provider.BlockToolUse, ID: "call_0", Name: "bash", Input: json.RawMessage(`{"command":"pwd"}`)},
		}},
		{Role: provider.RoleUser, Content: []provider.Block{
			{Type: provider.BlockToolResult, ToolUseID: "call_0", Text: "/tmp"},
		}},
	}
	res, err := c.Complete(context.Background(), provider.Request{
		Model:    "m",
		Messages: history,
		Tools:    []provider.ToolDef{{Name: "bash", Schema: json.RawMessage(`{"type":"object"}`)}},
	}, func(ev provider.Event) {
		if ev.Type == "tool_start" {
			started = ev.Tool
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "bash" || got.Tools[0].Type != "function" {
		t.Errorf("tools = %+v", got.Tools)
	}
	types := []string{}
	for _, it := range got.Input {
		types = append(types, it.Type)
	}
	if strings.Join(types, ",") != "message,function_call,function_call_output" {
		t.Errorf("input types = %v", types)
	}
	if got.Input[2].CallID != "call_0" || got.Input[2].Output != "/tmp" {
		t.Errorf("tool output item = %+v", got.Input[2])
	}
	if started != "bash" {
		t.Errorf("tool_start = %q", started)
	}
	uses := res.Message.ToolUses()
	if len(uses) != 1 || uses[0].ID != "call_1" || string(uses[0].Input) != `{"command":"ls"}` {
		t.Errorf("tool uses = %+v", uses)
	}
	if res.StopReason != provider.StopToolUse {
		t.Errorf("stop = %q", res.StopReason)
	}
}

func TestCompleteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.failed", `{"type":"response.failed","error":{"message":"boom"}}`)
	}))
	defer srv.Close()

	c := New("", srv.URL+"/v1", "")
	_, err := c.Complete(context.Background(), provider.Request{Model: "m"}, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestRetryOn500(t *testing.T) {
	old := retryDelays
	retryDelays = []time.Duration{0}
	defer func() { retryDelays = old }()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "oops", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.completed", `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`)
	}))
	defer srv.Close()

	c := New("", srv.URL+"/v1", "")
	res, err := c.Complete(context.Background(), provider.Request{Model: "m"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || res.Message.TextContent() != "ok" {
		t.Errorf("calls = %d, text = %q", calls, res.Message.TextContent())
	}
}

func TestBuildRequestReasoning(t *testing.T) {
	on := buildRequest(provider.Request{Model: "m", Reasoning: "medium"})
	if on.Reasoning == nil || on.Reasoning.Effort != "medium" {
		t.Fatalf("reasoning effort not set: %+v", on.Reasoning)
	}
	off := buildRequest(provider.Request{Model: "m", Reasoning: "off"})
	if off.Reasoning != nil {
		t.Fatalf("reasoning should be nil when off: %+v", off.Reasoning)
	}
	none := buildRequest(provider.Request{Model: "m"})
	if none.Reasoning != nil {
		t.Fatalf("reasoning should be nil by default: %+v", none.Reasoning)
	}
}
