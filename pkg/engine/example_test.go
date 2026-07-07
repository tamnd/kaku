package engine_test

import (
	"context"
	"fmt"
	"os"

	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/perm"
	"github.com/tamnd/kaku/pkg/provider/anthropic"
	"github.com/tamnd/kaku/pkg/tool"
	"github.com/tamnd/kaku/pkg/tool/builtin"
)

// Example shows the whole SDK surface: build a provider, a tool registry,
// and a permission engine, then run a prompt. The CLI, TUI, HTTP server,
// and MCP server are all thin adapters over exactly this.
func Example() {
	dir, _ := os.Getwd()

	reg := tool.NewRegistry(builtin.All(dir, nil, nil)...)
	a := &engine.Agent{
		Provider:  anthropic.New(os.Getenv("ANTHROPIC_API_KEY"), ""),
		Model:     "claude-sonnet-5",
		MaxTokens: 8192,
		System:    engine.DefaultSystem(dir),
		Tools:     reg,
		Perm: &perm.Engine{
			Mode:     perm.ModeAsk,
			Allow:    perm.ParseRules([]string{"bash(go test *)"}),
			ReadOnly: reg.ReadOnly,
		},
		// Ask decides tool calls the rules above did not settle.
		Ask: func(toolName, arg string) engine.Answer {
			return engine.Answer{Allow: toolName != "bash"}
		},
		// OnEvent streams progress; leave it nil to just get the answer.
		OnEvent: func(e engine.Event) {
			if e.Type == "tool_start" {
				fmt.Printf("running %s\n", e.Tool)
			}
		},
	}

	out, err := a.Run(context.Background(), "what does this package do?")
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(out)

	// a.Messages now holds the conversation; call Run again to continue it,
	// or hand the slice to pkg/session to persist it.
}
