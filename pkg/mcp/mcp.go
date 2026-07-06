// Package mcp implements a Model Context Protocol client over stdio and
// streamable HTTP transports, exposing remote tools to the tool registry.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/tamnd/kaku/pkg/config"
)

const protocolVersion = "2025-03-26"

// ToolInfo describes one tool advertised by an MCP server.
type ToolInfo struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// transport moves single JSON-RPC messages to and from one server.
type transport interface {
	// roundTrip sends a request that already carries an id and returns the
	// matching response.
	roundTrip(ctx context.Context, req *rpcRequest) (*rpcResponse, error)
	// notify sends a message that expects no response.
	notify(ctx context.Context, req *rpcRequest) error
	close() error
}

// Client is a connection to one MCP server.
type Client struct {
	name   string
	tr     transport
	nextID atomic.Int64
}

// Dial connects to the server described by cfg and runs the initialize
// handshake. cfg.URL selects the HTTP transport, cfg.Command stdio.
func Dial(ctx context.Context, name string, cfg config.MCPServer) (*Client, error) {
	var tr transport
	var err error
	switch {
	case cfg.URL != "":
		tr = newHTTPTransport(cfg.URL)
	case cfg.Command != "":
		tr, err = newStdioTransport(cfg)
	default:
		err = errors.New("neither command nor url set")
	}
	if err != nil {
		return nil, fmt.Errorf("mcp %s: %w", name, err)
	}
	c := &Client{name: name, tr: tr}
	initParams := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "kaku", "version": "0.1.0"},
	}
	if _, err := c.call(ctx, "initialize", initParams); err != nil {
		tr.close()
		return nil, err
	}
	note := &rpcRequest{JSONRPC: "2.0", Method: "notifications/initialized", Params: map[string]any{}}
	if err := tr.notify(ctx, note); err != nil {
		tr.close()
		return nil, fmt.Errorf("mcp %s: %w", name, err)
	}
	return c, nil
}

// Name returns the configured server name.
func (c *Client) Name() string { return c.name }

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	req := &rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}
	resp, err := c.tr.roundTrip(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("mcp %s: %w", c.name, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("mcp %s: %s", c.name, resp.Error.Message)
	}
	return resp.Result, nil
}

// Tools lists the tools the server offers.
func (c *Client) Tools(ctx context.Context) ([]ToolInfo, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp %s: tools/list: %w", c.name, err)
	}
	infos := make([]ToolInfo, 0, len(result.Tools))
	for _, t := range result.Tools {
		schema := t.InputSchema
		if len(schema) == 0 || string(schema) == "null" {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		infos = append(infos, ToolInfo{Name: t.Name, Description: t.Description, Schema: schema})
	}
	return infos, nil
}

// Call invokes one remote tool and returns its text output.
func (c *Client) Call(ctx context.Context, toolName string, args json.RawMessage) (string, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	params := map[string]any{"name": toolName, "arguments": args}
	raw, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return "", err
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("mcp %s: tools/call: %w", c.name, err)
	}
	parts := make([]string, 0, len(result.Content))
	for _, item := range result.Content {
		if item.Type == "text" {
			parts = append(parts, item.Text)
		} else {
			parts = append(parts, "["+item.Type+" content]")
		}
	}
	text := strings.Join(parts, "\n")
	if result.IsError {
		return "", fmt.Errorf("mcp %s: %s", c.name, text)
	}
	return text, nil
}

// Close shuts the transport down. For stdio servers the process gets
// SIGTERM and two seconds before a kill.
func (c *Client) Close() error {
	return c.tr.close()
}
