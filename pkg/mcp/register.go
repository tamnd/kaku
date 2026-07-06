package mcp

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/tamnd/kaku/pkg/config"
	"github.com/tamnd/kaku/pkg/tool"
)

// Register dials every configured server and adds its tools to reg under
// the name mcp__<server>__<tool>. Servers that fail land in the returned
// error map; the rest still register. The returned func closes every
// successful connection.
func Register(ctx context.Context, servers map[string]config.MCPServer, reg *tool.Registry) (func(), map[string]error) {
	errs := map[string]error{}
	var clients []*Client

	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		c, err := Dial(ctx, name, servers[name])
		if err != nil {
			errs[name] = err
			continue
		}
		infos, err := c.Tools(ctx)
		if err != nil {
			errs[name] = err
			c.Close()
			continue
		}
		clients = append(clients, c)
		for _, info := range infos {
			reg.Add(remoteTool{client: c, info: info})
		}
	}

	closeAll := func() {
		for _, c := range clients {
			c.Close()
		}
	}
	return closeAll, errs
}

type remoteTool struct {
	client *Client
	info   ToolInfo
}

func (t remoteTool) Name() string { return "mcp__" + t.client.Name() + "__" + t.info.Name }
func (t remoteTool) Description() string {
	return "(" + t.client.Name() + " MCP) " + t.info.Description
}
func (t remoteTool) Schema() json.RawMessage {
	return t.info.Schema
}
func (t remoteTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	return t.client.Call(ctx, t.info.Name, input)
}
