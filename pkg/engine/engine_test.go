package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamnd/kaku/pkg/perm"
	"github.com/tamnd/kaku/pkg/provider"
	"github.com/tamnd/kaku/pkg/tool"
)

// fakeProvider replays scripted responses.
type fakeProvider struct {
	resps []*provider.Response
	reqs  []provider.Request
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Complete(_ context.Context, req provider.Request, on func(provider.Event)) (*provider.Response, error) {
	f.reqs = append(f.reqs, req)
	if len(f.resps) == 0 {
		return nil, errors.New("fake provider exhausted")
	}
	r := f.resps[0]
	f.resps = f.resps[1:]
	if on != nil {
		on(provider.Event{Type: "text", Text: r.Message.TextContent()})
	}
	return r, nil
}

func toolUseResp(id, name, input string) *provider.Response {
	return &provider.Response{
		Message: provider.Message{Role: provider.RoleAssistant, Content: []provider.Block{
			{Type: provider.BlockToolUse, ID: id, Name: name, Input: json.RawMessage(input)},
		}},
		StopReason: provider.StopToolUse,
		Usage:      provider.Usage{InputTokens: 10, OutputTokens: 5},
	}
}

func textResp(text string) *provider.Response {
	return &provider.Response{
		Message:    provider.Text(provider.RoleAssistant, text),
		StopReason: provider.StopEndTurn,
		Usage:      provider.Usage{InputTokens: 10, OutputTokens: 5},
	}
}

func echoTool(safe bool) tool.Tool {
	return tool.Func{
		ToolName:    "echo",
		Desc:        "echoes its input",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Safe:        safe,
		Fn: func(_ context.Context, input json.RawMessage) (string, error) {
			return "echo:" + string(input), nil
		},
	}
}

func newAgent(p provider.Provider, tools *tool.Registry, mode perm.Mode) *Agent {
	pe := &perm.Engine{Mode: mode, ReadOnly: tools.ReadOnly}
	return &Agent{Provider: p, Model: "m", MaxTokens: 100, Tools: tools, Perm: pe}
}

func TestRunToolLoop(t *testing.T) {
	fp := &fakeProvider{resps: []*provider.Response{
		toolUseResp("t1", "echo", `{"x":1}`),
		textResp("done"),
	}}
	a := newAgent(fp, tool.NewRegistry(echoTool(true)), perm.ModeAsk)

	out, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if out != "done" {
		t.Fatalf("out = %q", out)
	}
	// user, assistant tool_use, user tool_result, assistant done
	if len(a.Messages) != 4 {
		t.Fatalf("messages = %d", len(a.Messages))
	}
	res := a.Messages[2].Content[0]
	if res.Type != provider.BlockToolResult || res.ToolUseID != "t1" {
		t.Fatalf("bad tool result block: %+v", res)
	}
	if !strings.Contains(res.Text, `echo:{"x":1}`) {
		t.Fatalf("tool result text = %q", res.Text)
	}
	if a.Usage.OutputTokens != 10 {
		t.Fatalf("usage not accumulated: %+v", a.Usage)
	}
	// The second request must include the tool results.
	if len(fp.reqs[1].Messages) != 3 {
		t.Fatalf("second request messages = %d", len(fp.reqs[1].Messages))
	}
}

func TestRunDeniesWithoutAsker(t *testing.T) {
	fp := &fakeProvider{resps: []*provider.Response{
		toolUseResp("t1", "echo", `{}`),
		textResp("gave up"),
	}}
	// echo is not read-only here, mode ask, no Ask func: must deny.
	a := newAgent(fp, tool.NewRegistry(echoTool(false)), perm.ModeAsk)

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	res := a.Messages[2].Content[0]
	if !res.IsError || !strings.Contains(res.Text, "permission") {
		t.Fatalf("expected permission error result, got %+v", res)
	}
}

func TestRunPlanModeAllowsReadOnly(t *testing.T) {
	fp := &fakeProvider{resps: []*provider.Response{
		toolUseResp("t1", "echo", `{}`),
		textResp("ok"),
	}}
	a := newAgent(fp, tool.NewRegistry(echoTool(true)), perm.ModePlan)
	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if res := a.Messages[2].Content[0]; res.IsError {
		t.Fatalf("read-only tool should run in plan mode: %+v", res)
	}
}

func TestAskAlwaysAddsRule(t *testing.T) {
	fp := &fakeProvider{resps: []*provider.Response{
		toolUseResp("t1", "echo", `{}`),
		toolUseResp("t2", "echo", `{}`),
		textResp("ok"),
	}}
	asked := 0
	a := newAgent(fp, tool.NewRegistry(echoTool(false)), perm.ModeAsk)
	a.Ask = func(name, arg string) Answer {
		asked++
		return Answer{Allow: true, Always: true}
	}
	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if asked != 1 {
		t.Fatalf("asked %d times, want 1 (always rule should cover the second call)", asked)
	}
}

func TestUnknownTool(t *testing.T) {
	fp := &fakeProvider{resps: []*provider.Response{
		toolUseResp("t1", "nope", `{}`),
		textResp("ok"),
	}}
	a := newAgent(fp, tool.NewRegistry(), perm.ModeAuto)
	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if res := a.Messages[2].Content[0]; !res.IsError || !strings.Contains(res.Text, "unknown tool") {
		t.Fatalf("expected unknown tool result, got %+v", res)
	}
}

func TestMaxTurns(t *testing.T) {
	fp := &fakeProvider{resps: []*provider.Response{
		toolUseResp("t1", "echo", `{}`),
		toolUseResp("t2", "echo", `{}`),
		toolUseResp("t3", "echo", `{}`),
	}}
	a := newAgent(fp, tool.NewRegistry(echoTool(true)), perm.ModeAuto)
	a.MaxTurns = 2
	if _, err := a.Run(context.Background(), "go"); err == nil {
		t.Fatal("expected max turns error")
	}
}

func TestSnapshotBeforeFirstMutatingTool(t *testing.T) {
	fp := &fakeProvider{resps: []*provider.Response{
		toolUseResp("t1", "look", `{}`),
		toolUseResp("t2", "echo", `{}`),
		toolUseResp("t3", "echo", `{}`),
		textResp("done"),
	}}
	look := tool.Func{
		ToolName:    "look",
		Desc:        "read-only",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Safe:        true,
		Fn: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "seen", nil
		},
	}
	a := newAgent(fp, tool.NewRegistry(look, echoTool(false)), perm.ModeAuto)
	var labels []string
	a.Snapshot = func(label string) error {
		labels = append(labels, label)
		return nil
	}
	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	// Not for the read-only tool, once for the two mutating calls.
	if len(labels) != 1 || labels[0] != "go" {
		t.Fatalf("labels = %v", labels)
	}
}

func TestSnapshotSkippedForReadOnlyRun(t *testing.T) {
	fp := &fakeProvider{resps: []*provider.Response{
		toolUseResp("t1", "echo", `{}`),
		textResp("done"),
	}}
	a := newAgent(fp, tool.NewRegistry(echoTool(true)), perm.ModeAuto)
	called := 0
	a.Snapshot = func(string) error { called++; return nil }
	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Fatalf("snapshot ran %d times for a read-only run", called)
	}
}

func TestParallelToolBatch(t *testing.T) {
	// Three read-only calls in one response: each blocks until all three
	// have started. Sequential execution would time out.
	const n = 3
	resp := &provider.Response{
		Message:    provider.Message{Role: provider.RoleAssistant},
		StopReason: provider.StopToolUse,
	}
	for i := range n {
		resp.Message.Content = append(resp.Message.Content, provider.Block{
			Type: provider.BlockToolUse, ID: fmt.Sprintf("t%d", i), Name: "wait", Input: json.RawMessage(`{}`),
		})
	}
	fp := &fakeProvider{resps: []*provider.Response{resp, textResp("done")}}

	started := make(chan struct{}, n)
	release := make(chan struct{})
	var once sync.Once
	waitTool := tool.Func{
		ToolName:    "wait",
		Desc:        "blocks until the whole batch started",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Safe:        true,
		Fn: func(_ context.Context, _ json.RawMessage) (string, error) {
			started <- struct{}{}
			if len(started) == n {
				once.Do(func() { close(release) })
			}
			select {
			case <-release:
				return "ok", nil
			case <-time.After(5 * time.Second):
				return "", errors.New("batch did not run concurrently")
			}
		},
	}

	a := newAgent(fp, tool.NewRegistry(waitTool), perm.ModeAuto)
	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	// Results arrive in call order and none timed out.
	res := a.Messages[2].Content
	if len(res) != n {
		t.Fatalf("results = %d", len(res))
	}
	for i, b := range res {
		if b.ToolUseID != fmt.Sprintf("t%d", i) {
			t.Fatalf("result %d out of order: %+v", i, b)
		}
		if b.IsError {
			t.Fatalf("result %d errored: %s", i, b.Text)
		}
	}
}

func TestMutatingToolsStaySequential(t *testing.T) {
	// Two mutating calls in one response must not overlap.
	resp := &provider.Response{
		Message: provider.Message{Role: provider.RoleAssistant, Content: []provider.Block{
			{Type: provider.BlockToolUse, ID: "t1", Name: "mut", Input: json.RawMessage(`{}`)},
			{Type: provider.BlockToolUse, ID: "t2", Name: "mut", Input: json.RawMessage(`{}`)},
		}},
		StopReason: provider.StopToolUse,
	}
	fp := &fakeProvider{resps: []*provider.Response{resp, textResp("done")}}

	var mu sync.Mutex
	running, maxRunning := 0, 0
	mutTool := tool.Func{
		ToolName:    "mut",
		Desc:        "mutates",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Fn: func(_ context.Context, _ json.RawMessage) (string, error) {
			mu.Lock()
			running++
			if running > maxRunning {
				maxRunning = running
			}
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			running--
			mu.Unlock()
			return "ok", nil
		},
	}

	a := newAgent(fp, tool.NewRegistry(mutTool), perm.ModeAuto)
	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if maxRunning != 1 {
		t.Fatalf("mutating tools overlapped: max concurrent = %d", maxRunning)
	}
}

func TestEventsStream(t *testing.T) {
	fp := &fakeProvider{resps: []*provider.Response{
		toolUseResp("t1", "echo", `{}`),
		textResp("done"),
	}}
	var types []string
	a := newAgent(fp, tool.NewRegistry(echoTool(true)), perm.ModeAuto)
	a.OnEvent = func(e Event) { types = append(types, e.Type) }
	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(types, ",")
	for _, want := range []string{"tool_start", "tool_end", "turn", "text"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s event in %s", want, joined)
		}
	}
}

func TestDegenerateReply(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{`{"name":"read","arguments":{"file_path":"cache.go"_}`, true},
		{"```json\n{\"name\":\"edit\",\"arguments\":{\"path\":\"x\"}}\n```", true},
		{`{"content":"User is working on a Go cache package."}`, true},
		{"The fix is done, all tests pass.", false},
		{`{"total": 7, "top": ["/api/users"]}`, false},
		{"Use `{\"name\": ...}` in your config.", false},
	}
	for _, c := range cases {
		if got := degenerateReply(c.text); got != c.want {
			t.Errorf("degenerateReply(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestRunNudgesSerializedToolCall(t *testing.T) {
	fp := &fakeProvider{resps: []*provider.Response{
		textResp(`{"name":"echo","arguments":{"x":1}}`),
		textResp("done"),
	}}
	a := newAgent(fp, tool.NewRegistry(echoTool(true)), perm.ModeAsk)

	out, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if out != "done" {
		t.Fatalf("out = %q", out)
	}
	// user, degenerate assistant, nudge user, assistant done
	if len(a.Messages) != 4 {
		t.Fatalf("messages = %d", len(a.Messages))
	}
	if a.Messages[2].Role != provider.RoleUser || a.Messages[2].TextContent() != nudgeMessage {
		t.Fatalf("expected nudge message, got %+v", a.Messages[2])
	}
}

func TestRunNudgeGivesUp(t *testing.T) {
	junk := `{"content":"noise"}`
	fp := &fakeProvider{resps: []*provider.Response{
		textResp(junk), textResp(junk), textResp(junk),
	}}
	a := newAgent(fp, tool.NewRegistry(echoTool(true)), perm.ModeAsk)

	out, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if out != junk {
		t.Fatalf("out = %q, want the junk back after nudges run out", out)
	}
	if len(fp.reqs) != 3 {
		t.Fatalf("requests = %d, want 3 (initial + 2 nudges)", len(fp.reqs))
	}
}
