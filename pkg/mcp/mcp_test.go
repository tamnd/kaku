package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/kaku/pkg/config"
	"github.com/tamnd/kaku/pkg/tool"
)

// TestHelperProcess is not a real test: the stdio tests re-exec the test
// binary with GO_WANT_HELPER_PROCESS=1 to run it as a tiny MCP server that
// speaks newline-delimited JSON-RPC on stdio.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	out := bufio.NewWriter(os.Stdout)
	reply := func(id int64, result string) {
		fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%d,"result":%s}`+"\n", id, result)
		out.Flush()
	}
	for sc.Scan() {
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil || req.ID == nil {
			continue // notification
		}
		switch req.Method {
		case "initialize":
			reply(*req.ID, `{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"helper","version":"0"}}`)
		case "tools/list":
			reply(*req.ID, `{"tools":[{"name":"echo","description":"Echo a message","inputSchema":{"type":"object","properties":{"msg":{"type":"string"}}}},{"name":"boom","description":"Always fails"}]}`)
		case "tools/call":
			if req.Params.Name == "boom" {
				reply(*req.ID, `{"content":[{"type":"text","text":"it broke"}],"isError":true}`)
				continue
			}
			var args struct {
				Msg string `json:"msg"`
			}
			json.Unmarshal(req.Params.Arguments, &args)
			result, _ := json.Marshal(map[string]any{
				"content": []map[string]any{{"type": "text", "text": "echo: " + args.Msg}},
			})
			reply(*req.ID, string(result))
		}
	}
	os.Exit(0)
}

func helperServer() config.MCPServer {
	return config.MCPServer{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	}
}

func TestStdioClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := Dial(ctx, "helper", helperServer())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	infos, err := c.Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 || infos[0].Name != "echo" {
		t.Fatalf("tools = %+v", infos)
	}
	// A tool with no inputSchema gets the empty-object default.
	if string(infos[1].Schema) != `{"type":"object"}` {
		t.Errorf("default schema = %s", infos[1].Schema)
	}

	out, err := c.Call(ctx, "echo", json.RawMessage(`{"msg":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "echo: hi" {
		t.Errorf("out = %q", out)
	}

	// isError results surface as errors carrying the text.
	_, err = c.Call(ctx, "boom", nil)
	if err == nil || !strings.Contains(err.Error(), "it broke") {
		t.Errorf("err = %v", err)
	}

	if err := c.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestStdioDialFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := Dial(ctx, "bad", config.MCPServer{Command: "/nonexistent-kaku-mcp"})
	if err == nil {
		t.Fatal("expected dial error")
	}
	_, err = Dial(ctx, "none", config.MCPServer{})
	if err == nil || !strings.Contains(err.Error(), "neither command nor url") {
		t.Fatalf("err = %v", err)
	}
}

// mcpHTTPServer is a minimal streamable HTTP MCP server for tests. It
// assigns a session id on initialize and requires it afterwards.
func mcpHTTPServer(t *testing.T, sse bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.ID == nil { // notification
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if req.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != "sess-42" {
			http.Error(w, "missing session", http.StatusBadRequest)
			return
		}
		var result string
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-42")
			result = `{"protocolVersion":"2025-03-26","capabilities":{}}`
		case "tools/list":
			result = `{"tools":[{"name":"add","description":"Add numbers","inputSchema":{"type":"object"}}]}`
		case "tools/call":
			var args struct{ A, B int }
			json.Unmarshal(req.Params.Arguments, &args)
			result = fmt.Sprintf(`{"content":[{"type":"text","text":"%d"}]}`, args.A+args.B)
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
			return
		}
		body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, *req.ID, result)
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
}

func TestHTTPClient(t *testing.T) {
	for _, sse := range []bool{false, true} {
		name := "json"
		if sse {
			name = "sse"
		}
		t.Run(name, func(t *testing.T) {
			srv := mcpHTTPServer(t, sse)
			defer srv.Close()

			ctx := context.Background()
			c, err := Dial(ctx, "web", config.MCPServer{URL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()

			infos, err := c.Tools(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(infos) != 1 || infos[0].Name != "add" {
				t.Fatalf("tools = %+v", infos)
			}

			out, err := c.Call(ctx, "add", json.RawMessage(`{"a":2,"b":3}`))
			if err != nil {
				t.Fatal(err)
			}
			if out != "5" {
				t.Errorf("out = %q", out)
			}
		})
	}
}

func TestRegister(t *testing.T) {
	srv := mcpHTTPServer(t, false)
	defer srv.Close()

	reg := tool.NewRegistry()
	servers := map[string]config.MCPServer{
		"web":  {URL: srv.URL},
		"dead": {Command: "/nonexistent-kaku-mcp"},
	}
	closeAll, errs := Register(context.Background(), servers, reg)
	defer closeAll()

	if len(errs) != 1 || errs["dead"] == nil {
		t.Errorf("errs = %v", errs)
	}
	tl, ok := reg.Get("mcp__web__add")
	if !ok {
		t.Fatalf("tool not registered: %v", reg.List())
	}
	if !strings.Contains(tl.Description(), "web MCP") {
		t.Errorf("desc = %q", tl.Description())
	}
	out, err := tl.Run(context.Background(), json.RawMessage(`{"a":4,"b":6}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "10" {
		t.Errorf("out = %q", out)
	}
}
