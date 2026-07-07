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
| `kaku init` | Scan the repo and write a starter `KAKU.md`: detected toolchain, build and test commands, layout, and a conventions placeholder. Later runs load `KAKU.md` as project instructions. |
| `kaku sessions` | List this project's sessions. Subcommands: `rename <id> <title>`, `delete <id>` (`--force` skips the prompt), `export <id>` (`--format md\|html\|json`, `-o file`). |
| `kaku models` | List every model kaku can resolve, the default first, then each named provider's models. |
| `kaku rewind [checkpoint]` | Restore the working tree to a checkpoint; `--list` shows them. |
| `kaku serve` | HTTP API over one conversation with SSE streaming; `--addr` sets the listen address (default `127.0.0.1:8377`). |
| `kaku mcp` | Speak MCP on stdio, exposing the agent as a `kaku` tool. |

## Shared flags

These work on the root command and on `serve` and `mcp`:

| Flag | Meaning |
|---|---|
| `-C, --dir` | Work in this directory instead of the current one. |
| `--model` | Model reference: `provider/model`, a bare `model`, or `model:level`. A named provider is looked up in the config's `providers` map; a bare name falls back to the default provider. |
| `--provider` | `anthropic`, `openai`, or `responses`. |
| `--base-url` | API base URL, for local servers and proxies. |
| `--api-key-env` | Environment variable holding the API key. |
| `--thinking` | Reasoning level for this run: `off`, `minimal`, `low`, `medium`, `high`, or `xhigh`. Overrides the model default and the config `reasoning` key. |
| `--hide-thinking` | Do not print thinking, even when reasoning is on. |
| `--mode` | Permission mode: `plan`, `ask`, or `auto`. |
| `-c, --continue` | Continue the newest session in this project. `--resume` is a kept alias. |
| `--session <id>` | Continue a specific session. |
| `--fork <id>` | Copy a session into a new one and continue from the copy. The original is left untouched. |
| `--no-session` | Run without reading or writing a session file: nothing is persisted. |
| `--title <str>` | Set the session title up front instead of deriving it from the first prompt. |
| `--output-format <text\|json>` | Headless output format. `text` (default) is human lines; `json` emits one event object per line. |
| `--json` | Shorthand for `--output-format json`. |
| `--max-turns` | Cap on model turns per run. |
| `--no-mcp` | Skip connecting configured MCP servers. |
| `--sandbox` | Confine bash writes to the working directory. |
| `--tools <list>` | Allowlist of tools by name glob, comma separated. Only these are offered to the model (e.g. `read,grep,glob,ls` for a read-only run). |
| `--exclude-tools <list>` | Denylist of tools by name glob, comma separated. |
| `--no-tools` | Run with no tools at all. |
| `--no-builtin-tools` | Drop the builtin tools but keep MCP and the agent tool. |

## Headless JSON output

`kaku -p --json "..."` streams one JSON object per line so a program can drive the agent.
The first line is a session header, then one object per event, and the last line on success is a `result` with the final text and token totals.
On failure the last line is an `error` object and the exit code is non-zero.

```
{"type":"session","id":"20260707-...","model":"claude-sonnet-5","cwd":"/work"}
{"type":"text","text":"Looking at the code"}
{"type":"tool_start","tool":"read","input":{"path":"main.go"}}
{"type":"tool_end","tool":"read","output":"..."}
{"type":"turn","input_tokens":1200,"output_tokens":340}
{"type":"result","text":"Done.","input_tokens":1200,"output_tokens":340}
```

A consumer that only wants the answer reads the last line:

```bash
kaku -p --json "list the go files" | jq -c 'select(.type=="result") | .text'
```

## TUI commands

| Command | What it does |
|---|---|
| `/model [name]` | Switch the model. With no name it opens a picker over the models in your config; move with the arrow keys, `enter` selects, `esc` cancels. A name that does not resolve fails in a dialog instead of poisoning the next request. |
| `/compact` | Summarize old turns now instead of waiting for the budget. |
| `/skills` | List available skills. |
| `/init` | Scan the repo and write a starter `KAKU.md` (toolchain, build and test commands, layout, a conventions placeholder). Same as `kaku init` on the CLI. |
| `/theme [name]` | Switch the color theme, or list the choices. Builtins are `dark` and `light`; custom themes load from `~/.kaku/themes/*.json` and `.kaku/themes/*.json`. |
| `/new` | Close the current session and start a fresh one. |
| `/sessions` | Open a picker over this project's saved sessions: `enter` switches, `d` deletes, `esc` closes. |
| `/name <title>` | Rename the current session. |
| `/export [file]` | Write the session to a file; the extension picks the format (`.md`, `.html`, `.json`), defaulting to `<id>.md`. |
| `/thinking [level]` | Show or set the reasoning level live (`off`, `minimal`, `low`, `medium`, `high`, `xhigh`); `shift+tab` cycles it. |
| `/clear` | Reset the in-memory conversation; the transcript file keeps its history. |
| `/help` | Open the command help dialog. |
| `/quit` | Exit. |
| `/<skill> [args]` | Run a skill from `.kaku/skills/` or `~/.kaku/skills/`. |

A line starting with `!` runs the rest under the shell in the working directory and
shows the output; a single `!` also feeds that output to your next prompt as context,
while `!!` runs quietly and feeds nothing. `ctrl+n` cycles the configured models.

Type `@` in the composer to open a fuzzy file picker over the repo (it skips `.git`,
`node_modules`, `vendor`, and `dist`): keep typing to filter, arrow keys to move,
`enter` or `tab` to insert the path, `esc` to close. On send, each `@path` inlines the
file's contents, so `explain @main.go` reaches the model with the file attached.

Dialogs (help, the model picker, errors, and permission prompts) open centered over
the transcript. A read-and-dismiss dialog closes on `esc` or `enter`; the picker
takes the arrow keys.

At a permission prompt: `y` allows once, `a` allows that tool for the session, `n` denies.
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
