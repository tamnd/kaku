---
title: "The HTTP server and the Go SDK"
description: "kaku serve streams engine events over SSE, and pkg/engine is the same loop as a library."
weight: 50
---

Every kaku surface is a thin adapter over one Go package.
When the TUI is the wrong shape, take the engine over HTTP, or import it.

## kaku serve

```bash
kaku serve --mode auto
# kaku serving /path/to/project on http://127.0.0.1:8377
```

The server exposes one agent conversation, like a TUI session you talk to with curl:

- `POST /v1/messages` with `{"prompt": "..."}` answers as a stream of Server-Sent Events.
- `GET /v1/history` returns the conversation so far as JSON.
- `GET /healthz` answers `ok`.

```bash
curl -N localhost:8377/v1/messages -d '{"prompt":"run the tests and fix what breaks"}'
```

```text
event: tool_start
data: {"tool":"bash","input":{"command":"go test ./..."}}

event: tool_end
data: {"tool":"bash","output":"...","is_error":false}

event: text
data: {"text":"Two tests failed in pkg/parser; fixing the fixture..."}

event: done
data: {"output":"...","usage":{"input_tokens":3969,"output_tokens":56}}
```

The event names mirror the engine's own: `text`, `tool_start`, `tool_end`, `info`, then exactly one `done` or `error`.
Requests are serialized, so two clients cannot interleave one conversation.
The server is headless: ask-mode prompts degrade to deny, so pass `--mode auto` or allow rules for the tools it should use.
Bind stays on localhost by default; put a reverse proxy with auth in front before exposing it further.

## The Go SDK

`pkg/engine` is the loop itself:

```go
reg := tool.NewRegistry(builtin.All(dir)...)
a := &engine.Agent{
    Provider: anthropic.New(os.Getenv("ANTHROPIC_API_KEY"), ""),
    Model:    "claude-sonnet-5",
    System:   engine.DefaultSystem(dir),
    Tools:    reg,
    Perm: &perm.Engine{
        Mode:     perm.ModeAsk,
        Allow:    perm.ParseRules([]string{"bash(go test *)"}),
        ReadOnly: reg.ReadOnly,
    },
    Ask: func(toolName, arg string) engine.Answer {
        return engine.Answer{Allow: toolName != "bash"}
    },
    OnEvent: func(e engine.Event) {
        if e.Type == "tool_start" {
            log.Printf("running %s", e.Tool)
        }
    },
}

out, err := a.Run(ctx, "what does this package do?")
```

Everything is a field, nothing is global:

- `Provider` is an interface with three implementations (`anthropic`, `openai`, `responses`); implement it to add a fourth.
- `Tools` is a registry; add your own `tool.Func` next to the builtins.
- `Perm` plus `Ask` decide what runs; `OnEvent` streams progress; leave it nil to just get the answer.
- `a.Messages` holds the conversation after a run; call `Run` again to continue it, or hand the slice to `pkg/session` to persist it.

The runnable version of this example lives in [pkg/engine/example_test.go](https://github.com/tamnd/kaku/blob/main/pkg/engine/example_test.go), and `cmd/kaku` is the reference consumer: the TUI, headless mode, `serve`, and `mcp` wire the same struct four ways.
