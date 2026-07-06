package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// ServerTool is one tool exposed by Serve.
type ServerTool struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Run         func(ctx context.Context, args json.RawMessage) (string, error)
}

// Serve speaks MCP over newline-delimited JSON-RPC on in and out, the stdio
// transport, until in reaches EOF or ctx is cancelled. It is the client
// half of this package in reverse: kaku itself becomes an MCP server.
func Serve(ctx context.Context, in io.Reader, out io.Writer, version string, tools []ServerTool) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	reply := func(id *int64, result any, rerr *rpcError) {
		if id == nil {
			return
		}
		data, err := json.Marshal(result)
		if err != nil {
			data = nil
			rerr = &rpcError{Code: -32603, Message: err.Error()}
		}
		resp := rpcResponse{JSONRPC: "2.0", ID: id, Result: data, Error: rerr}
		if rerr != nil {
			resp.Result = nil
		}
		line, _ := json.Marshal(resp)
		fmt.Fprintf(out, "%s\n", line)
	}

	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		switch req.Method {
		case "initialize":
			reply(req.ID, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "kaku", "version": version},
			}, nil)
		case "ping":
			reply(req.ID, map[string]any{}, nil)
		case "tools/list":
			list := make([]map[string]any, 0, len(tools))
			for _, t := range tools {
				schema := t.Schema
				if len(schema) == 0 {
					schema = json.RawMessage(`{"type":"object"}`)
				}
				list = append(list, map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"inputSchema": schema,
				})
			}
			reply(req.ID, map[string]any{"tools": list}, nil)
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				reply(req.ID, nil, &rpcError{Code: -32602, Message: "bad tools/call params"})
				continue
			}
			var tl *ServerTool
			for i := range tools {
				if tools[i].Name == params.Name {
					tl = &tools[i]
					break
				}
			}
			if tl == nil {
				reply(req.ID, nil, &rpcError{Code: -32602, Message: "unknown tool " + params.Name})
				continue
			}
			text, err := tl.Run(ctx, params.Arguments)
			isErr := false
			if err != nil {
				text, isErr = err.Error(), true
			}
			reply(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": isErr,
			}, nil)
		default:
			// Notifications get no reply; unknown requests get an error.
			reply(req.ID, nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method})
		}
	}
	return sc.Err()
}
