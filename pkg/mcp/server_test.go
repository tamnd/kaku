package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func serveScript(t *testing.T, tools []ServerTool, lines ...string) []rpcResponse {
	t.Helper()
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	var out bytes.Buffer
	if err := Serve(context.Background(), in, &out, "test", tools); err != nil {
		t.Fatal(err)
	}
	var resps []rpcResponse
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		var r rpcResponse
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("bad response line %q: %v", sc.Text(), err)
		}
		resps = append(resps, r)
	}
	return resps
}

func TestServeHandshakeAndTools(t *testing.T) {
	tools := []ServerTool{{
		Name:        "kaku",
		Description: "runs the agent",
		Schema:      json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"}}}`),
		Run: func(_ context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Prompt string `json:"prompt"`
			}
			json.Unmarshal(args, &in)
			if in.Prompt == "boom" {
				return "", errors.New("it broke")
			}
			return "did: " + in.Prompt, nil
		},
	}}

	resps := serveScript(t, tools,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"kaku","arguments":{"prompt":"fix the test"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"kaku","arguments":{"prompt":"boom"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"ping"}`,
	)
	// The notification gets no reply: 6 responses for 7 lines.
	if len(resps) != 6 {
		t.Fatalf("responses = %d", len(resps))
	}

	var initRes struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(resps[0].Result, &initRes); err != nil {
		t.Fatal(err)
	}
	if initRes.ProtocolVersion != protocolVersion || initRes.ServerInfo.Name != "kaku" || initRes.ServerInfo.Version != "test" {
		t.Fatalf("initialize result = %+v", initRes)
	}

	var listRes struct {
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resps[1].Result, &listRes); err != nil {
		t.Fatal(err)
	}
	if len(listRes.Tools) != 1 || listRes.Tools[0].Name != "kaku" {
		t.Fatalf("tools/list = %+v", listRes)
	}

	var callRes struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resps[2].Result, &callRes); err != nil {
		t.Fatal(err)
	}
	if callRes.IsError || callRes.Content[0].Text != "did: fix the test" {
		t.Fatalf("tools/call = %+v", callRes)
	}

	if err := json.Unmarshal(resps[3].Result, &callRes); err != nil {
		t.Fatal(err)
	}
	if !callRes.IsError || callRes.Content[0].Text != "it broke" {
		t.Fatalf("failing tools/call = %+v", callRes)
	}

	if resps[4].Error == nil || !strings.Contains(resps[4].Error.Message, "unknown tool") {
		t.Fatalf("unknown tool response = %+v", resps[4])
	}
	if resps[5].Error != nil {
		t.Fatalf("ping errored: %+v", resps[5].Error)
	}
}

func TestServeUnknownMethod(t *testing.T) {
	resps := serveScript(t, nil,
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
	)
	if len(resps) != 1 || resps[0].Error == nil || resps[0].Error.Code != -32601 {
		t.Fatalf("responses = %+v", resps)
	}
}

func TestServeAgainstOwnClient(t *testing.T) {
	// The client half of this package should be able to talk to the server
	// half: round-trip through a real subprocess would need a binary, so
	// this wires the framing directly.
	tools := []ServerTool{{
		Name: "add",
		Run: func(_ context.Context, args json.RawMessage) (string, error) {
			var in struct{ A, B int }
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			return fmt.Sprint(in.A + in.B), nil
		},
	}}
	resps := serveScript(t, tools,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"add","arguments":{"A":2,"B":3}}}`,
	)
	var callRes struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resps[1].Result, &callRes); err != nil {
		t.Fatal(err)
	}
	if callRes.Content[0].Text != "5" {
		t.Fatalf("add = %+v", callRes)
	}
}
