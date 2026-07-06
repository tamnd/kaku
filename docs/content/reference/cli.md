---
title: "CLI reference"
description: "Every kaku command and flag."
weight: 10
---

```
kaku [command] [prompt] [--flags]
```

Run `kaku <command> --help` for the canonical, up-to-date list.

## Commands

| Command | What it does |
|---|---|
| `kaku` | Interactive TUI in the current project. |
| `kaku [prompt]` or `kaku -p "..."` | Headless run: stream the answer to stdout, tool activity to stderr, exit non-zero on failure. Piped stdin joins the prompt. |
| `kaku sessions` | List this project's sessions. |
| `kaku rewind [checkpoint]` | Restore the working tree to a checkpoint; `--list` shows them. |
| `kaku serve` | HTTP API over one conversation with SSE streaming; `--addr` sets the listen address (default `127.0.0.1:8377`). |
| `kaku mcp` | Speak MCP on stdio, exposing the agent as a `kaku` tool. |

## Shared flags

These work on the root command and on `serve` and `mcp`:

| Flag | Meaning |
|---|---|
| `-C, --dir` | Work in this directory instead of the current one. |
| `--model` | Model override. |
| `--provider` | `anthropic`, `openai`, or `responses`. |
| `--base-url` | API base URL, for local servers and proxies. |
| `--api-key-env` | Environment variable holding the API key. |
| `--mode` | Permission mode: `plan`, `ask`, or `auto`. |
| `--resume` | Continue the newest session in this project. |
| `--session <id>` | Continue a specific session. |
| `--max-turns` | Cap on model turns per run. |
| `--no-mcp` | Skip connecting configured MCP servers. |
| `--sandbox` | Confine bash writes to the working directory. |

## TUI commands

| Command | What it does |
|---|---|
| `/model [name]` | Show or switch the model. |
| `/compact` | Summarize old turns now instead of waiting for the budget. |
| `/skills` | List available skills. |
| `/clear` | Start a fresh conversation. |
| `/help` | List commands. |
| `/quit` | Exit. |
| `/<skill> [args]` | Run a skill from `.kaku/skills/` or `~/.kaku/skills/`. |

At a permission prompt: `y` allows once, `a` allows that tool for the session, `n` or `esc` denies.
`esc` interrupts a running turn.

## Builtin tools

| Tool | Kind | What the model can do with it |
|---|---|---|
| `read` | read-only | Read a file, with offset and limit for big ones. |
| `grep` | read-only | Search file contents; skips `.git`, `node_modules`, `vendor`, `dist`. |
| `glob` | read-only | Find files by pattern. |
| `ls` | read-only | List a directory. |
| `fetch` | read-only | GET a URL and return the body as text. |
| `write` | mutating | Create or overwrite a file. |
| `edit` | mutating | Replace an exact string in a file. |
| `bash` | mutating | Run a shell command in the working directory. |
| `agent` | read-only | Delegate a task to a subagent from `.kaku/agents/`. |

Read-only tools run without prompting in every mode; mutating tools are governed by the mode and rules.
Consecutive read-only calls in one turn run in parallel.
