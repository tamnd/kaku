package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

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
