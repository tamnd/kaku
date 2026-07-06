# kaku

Kaku (書く, "to write") is a coding agent that lives in your terminal.
One static Go binary, no runtime to install, no cloud lock-in.
Point it at the Anthropic API or at any OpenAI-compatible endpoint: chat completions or the Responses API, a llama.cpp or MLX server on your own hardware, or a hosted proxy.

Sibling of [yomi](https://github.com/tamnd/yomi): yomi reads the web, kaku writes code.

## Why another coding agent

Everything is a file and everything is a plugin.

- Sessions are JSONL you can cat, grep, and resume.
- Skills, subagents, and project memory are Markdown you can edit by hand.
- External tools arrive over MCP; the builtin set stays small and trusted.
- Permissions are explicit: plan, ask, or auto, with allow and deny rules in settings.
- The engine is a plain Go package; the CLI, TUI, and server are thin adapters over it.

## Status

Early development.
The core loop, builtin tools, providers, sessions, skills, hooks, MCP client, and TUI are landing now.

## Quick start

```sh
export ANTHROPIC_API_KEY=...
kaku                          # interactive TUI in the current project
kaku -p "fix the failing test"  # headless, prints the answer, exits
```

To run against a local model:

```sh
kaku --provider openai --base-url http://127.0.0.1:8080/v1 --model qwen3-30b
```

Servers that speak the newer OpenAI Responses API work too:

```sh
kaku --provider responses --base-url http://127.0.0.1:8000/v1 --model gpt-5
```

## Checkpoints

In a git repository, kaku snapshots the working tree before the first file-changing tool call of every turn.
Snapshots live under a hidden ref (`refs/kaku/checkpoint`), so your branches, index, and HEAD are never touched.

```sh
kaku rewind --list   # show checkpoints
kaku rewind          # restore the newest one
kaku rewind 8602b78  # restore a specific one
```

The state before a rewind is snapshotted too, so a rewind can itself be undone.

## Configuration

Global config lives in `~/.kaku/config.json`, per-project settings in `.kaku/settings.json`.
Project instructions are read from `KAKU.md`, and `AGENTS.md` or `CLAUDE.md` are picked up too, so kaku drops into existing repos.

## License

MIT
