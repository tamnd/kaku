package serve

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/perm"
	"github.com/tamnd/kaku/pkg/provider"
	"github.com/tamnd/kaku/pkg/tool"
)

type fakeProvider struct {
	resps []*provider.Response
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Complete(_ context.Context, req provider.Request, on func(provider.Event)) (*provider.Response, error) {
	if len(f.resps) == 0 {
		return nil, errors.New("fake provider exhausted")
	}
	r := f.resps[0]
	f.resps = f.resps[1:]
	if on != nil && r.Message.TextContent() != "" {
		on(provider.Event{Type: "text", Text: r.Message.TextContent()})
	}
	return r, nil
}

func testAgent(resps ...*provider.Response) *engine.Agent {
	reg := tool.NewRegistry(tool.Func{
		ToolName:    "echo",
		Desc:        "echoes its input",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Safe:        true,
		Fn: func(_ context.Context, input json.RawMessage) (string, error) {
			return "echo:" + string(input), nil
		},
	})
	pe := &perm.Engine{Mode: perm.ModeAuto, ReadOnly: reg.ReadOnly}
	return &engine.Agent{
		Provider:  &fakeProvider{resps: resps},
		Model:     "m",
		MaxTokens: 100,
		Tools:     reg,
		Perm:      pe,
	}
}

func TestMessageStreamsSSE(t *testing.T) {
	a := testAgent(
		&provider.Response{
			Message: provider.Message{Role: provider.RoleAssistant, Content: []provider.Block{
				{Type: provider.BlockToolUse, ID: "t1", Name: "echo", Input: json.RawMessage(`{"x":1}`)},
			}},
			StopReason: provider.StopToolUse,
		},
		&provider.Response{
			Message:    provider.Text(provider.RoleAssistant, "all done"),
			StopReason: provider.StopEndTurn,
			Usage:      provider.Usage{InputTokens: 10, OutputTokens: 5},
		},
	)
	srv := httptest.NewServer(Handler(a, nil))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(`{"prompt":"go"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	for _, want := range []string{
		"event: tool_start", `"tool":"echo"`,
		"event: tool_end", `"output":"echo:{\"x\":1}"`,
		"event: text", `"text":"all done"`,
		"event: done", `"output":"all done"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("stream missing %q:\n%s", want, s)
		}
	}

	// The conversation is visible afterwards.
	hist, err := http.Get(srv.URL + "/v1/history")
	if err != nil {
		t.Fatal(err)
	}
	defer hist.Body.Close()
	var msgs []provider.Message
	if err := json.NewDecoder(hist.Body).Decode(&msgs); err != nil {
		t.Fatal(err)
	}
	// user, assistant tool_use, user tool_result, assistant done
	if len(msgs) != 4 {
		t.Fatalf("history = %d messages", len(msgs))
	}
}

func TestMessageRequiresPrompt(t *testing.T) {
	srv := httptest.NewServer(Handler(testAgent(), nil))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestMessageProviderError(t *testing.T) {
	srv := httptest.NewServer(Handler(testAgent(), nil)) // no scripted responses
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(`{"prompt":"go"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "event: error") {
		t.Fatalf("stream missing error event:\n%s", body)
	}
}

func TestExpandApplied(t *testing.T) {
	a := testAgent(&provider.Response{
		Message:    provider.Text(provider.RoleAssistant, "ok"),
		StopReason: provider.StopEndTurn,
	})
	expand := func(s string) string { return "expanded: " + s }
	srv := httptest.NewServer(Handler(a, expand))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(`{"prompt":"/fix it"}`))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if got := a.Messages[0].TextContent(); got != "expanded: /fix it" {
		t.Fatalf("first message = %q", got)
	}
}

func TestHealthz(t *testing.T) {
	srv := httptest.NewServer(Handler(testAgent(), nil))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
