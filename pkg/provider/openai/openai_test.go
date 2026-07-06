package openai

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

func chunkLine(w http.ResponseWriter, data string) {
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func TestCompleteText(t *testing.T) {
	var got apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunkLine(w, `{"choices":[{"delta":{"role":"assistant"},"finish_reason":null}]}`)
		chunkLine(w, `{"choices":[{"delta":{"content":"hi "},"finish_reason":null}]}`)
		chunkLine(w, `{"choices":[{"delta":{"content":"there"},"finish_reason":null}]}`)
		chunkLine(w, `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2}}`)
		chunkLine(w, "[DONE]")
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
	if !got.Stream || !got.StreamOptions.IncludeUsage {
		t.Errorf("request = %+v", got)
	}
	if len(got.Messages) != 2 || got.Messages[0].Role != "system" || got.Messages[1].Role != "user" {
		t.Errorf("messages = %+v", got.Messages)
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
	if res.Usage.InputTokens != 8 || res.Usage.OutputTokens != 2 {
		t.Errorf("usage = %+v", res.Usage)
	}
}

func TestCompleteToolCalls(t *testing.T) {
	var got apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a := r.Header.Get("Authorization"); a != "" {
			t.Errorf("expected no auth header with empty key, got %q", a)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		// Arguments split across chunks, accumulated by index.
		chunkLine(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`)
		chunkLine(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":"}}]},"finish_reason":null}]}`)
		chunkLine(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]},"finish_reason":null}]}`)
		chunkLine(w, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
		chunkLine(w, "[DONE]")
	}))
	defer srv.Close()

	c := New("", srv.URL+"/v1", "local")
	var started string
	// History with a prior tool round, to exercise the request mapping.
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
	if c.Name() != "local" {
		t.Errorf("name = %q", c.Name())
	}
	if len(got.Tools) != 1 || got.Tools[0].Function.Name != "bash" {
		t.Errorf("tools = %+v", got.Tools)
	}
	// user, assistant with tool_calls, role:tool result.
	if len(got.Messages) != 3 {
		t.Fatalf("messages = %+v", got.Messages)
	}
	if len(got.Messages[1].ToolCalls) != 1 || got.Messages[1].ToolCalls[0].ID != "call_0" {
		t.Errorf("assistant tool_calls = %+v", got.Messages[1])
	}
	if got.Messages[2].Role != "tool" || got.Messages[2].ToolCallID != "call_0" {
		t.Errorf("tool message = %+v", got.Messages[2])
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
		chunkLine(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		chunkLine(w, "[DONE]")
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

func TestNoRetryOn400(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer srv.Close()

	c := New("", srv.URL+"/v1", "")
	_, err := c.Complete(context.Background(), provider.Request{Model: "m"}, nil)
	if err == nil || calls != 1 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
}
