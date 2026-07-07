package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
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

func testAgent(mode perm.Mode, resps ...*provider.Response) *engine.Agent {
	reg := tool.NewRegistry(tool.Func{
		ToolName:    "echo",
		Desc:        "echoes its input",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Safe:        false, // not read-only, so ask-mode prompts for it
		Fn: func(_ context.Context, input json.RawMessage) (string, error) {
			return "echo:" + string(input), nil
		},
	})
	pe := &perm.Engine{Mode: mode, ReadOnly: reg.ReadOnly}
	return &engine.Agent{
		Provider:  &fakeProvider{resps: resps},
		Model:     "m",
		MaxTokens: 100,
		Tools:     reg,
		Perm:      pe,
	}
}

// readLines decodes newline-delimited JSON objects off r until EOF, sending
// each into the returned channel.
func readLines(t *testing.T, r io.Reader) <-chan map[string]any {
	t.Helper()
	out := make(chan map[string]any, 64)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Errorf("bad output line %q: %v", line, err)
				return
			}
			out <- m
		}
	}()
	return out
}

func waitFor(t *testing.T, ch <-chan map[string]any, typ string) map[string]any {
	t.Helper()
	for m := range ch {
		if m["type"] == typ {
			return m
		}
	}
	t.Fatalf("output ended before a %q line arrived", typ)
	return nil
}

func TestPromptRunEmitsResponse(t *testing.T) {
	a := testAgent(perm.ModeAuto,
		&provider.Response{
			Message:    provider.Text(provider.RoleAssistant, "hello there"),
			StopReason: provider.StopEndTurn,
			Usage:      provider.Usage{InputTokens: 3, OutputTokens: 2},
		},
	)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := New(Options{Agent: a, Mode: "auto", Dir: "/w"})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); s.Serve(context.Background(), inR, outW); outW.Close() }()

	lines := readLines(t, outR)
	if ready := waitFor(t, lines, "ready"); ready["model"] != "m" {
		t.Errorf("ready model = %v, want m", ready["model"])
	}

	io.WriteString(inW, `{"type":"prompt","id":1,"text":"hi"}`+"\n")
	if txt := waitFor(t, lines, "text"); txt["text"] != "hello there" {
		t.Errorf("text = %v", txt["text"])
	}
	resp := waitFor(t, lines, "response")
	if resp["id"].(float64) != 1 || resp["text"] != "hello there" {
		t.Errorf("response = %+v", resp)
	}

	inW.Close()
	wg.Wait()
}

func TestPermissionRoundTrip(t *testing.T) {
	a := testAgent(perm.ModeAsk,
		&provider.Response{
			Message: provider.Message{Role: provider.RoleAssistant, Content: []provider.Block{
				{Type: provider.BlockToolUse, ID: "t1", Name: "echo", Input: json.RawMessage(`{"x":1}`)},
			}},
			StopReason: provider.StopToolUse,
		},
		&provider.Response{
			Message:    provider.Text(provider.RoleAssistant, "done"),
			StopReason: provider.StopEndTurn,
		},
	)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := New(Options{Agent: a, Mode: "ask", Dir: "/w"})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); s.Serve(context.Background(), inR, outW); outW.Close() }()

	lines := readLines(t, outR)
	waitFor(t, lines, "ready")

	io.WriteString(inW, `{"type":"prompt","id":1,"text":"run echo"}`+"\n")
	req := waitFor(t, lines, "permission_request")
	if req["tool"] != "echo" {
		t.Errorf("permission tool = %v, want echo", req["tool"])
	}
	id := int(req["id"].(float64))

	// Approve it, then the run should finish.
	resp := map[string]any{"type": "permission_response", "id": id, "allow": true}
	data, _ := json.Marshal(resp)
	io.WriteString(inW, string(data)+"\n")

	if end := waitFor(t, lines, "tool_end"); end["is_error"] == true {
		t.Errorf("tool should have run after approval: %+v", end)
	}
	if fin := waitFor(t, lines, "response"); fin["text"] != "done" {
		t.Errorf("final response = %+v", fin)
	}

	inW.Close()
	wg.Wait()
}

func TestGetState(t *testing.T) {
	a := testAgent(perm.ModeAuto)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := New(Options{Agent: a, Mode: "auto", Dir: "/proj"})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); s.Serve(context.Background(), inR, outW); outW.Close() }()

	lines := readLines(t, outR)
	waitFor(t, lines, "ready")

	io.WriteString(inW, `{"type":"get_state","id":9}`+"\n")
	st := waitFor(t, lines, "response")
	if st["id"].(float64) != 9 || st["cwd"] != "/proj" || st["model"] != "m" {
		t.Errorf("state = %+v", st)
	}

	inW.Close()
	wg.Wait()
}
