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
| `kaku sessions` | List this project's sessions. Subcommands: `rename <id> <title>`, `delete <id>` (`--force` skips the prompt), `export <id>` (`--format md\|html\|json`, `-o file`), `tree` (show the fork lineage indented), `share <id>` (write a self-contained HTML copy and print its path, `-o file`). |
| `kaku models` | List every model kaku can resolve, the default first, then each named provider's models. |
| `kaku auth` | Manage stored provider API keys in `~/.kaku/auth.json` (0600). Subcommands: `login [provider]` (reads a key without echoing it), `list` (provider names only, never keys), `logout [provider]`. Provider defaults to `anthropic`. A stored key fills in when the provider's environment variable is unset. |
| `kaku rewind [checkpoint]` | Restore the working tree to a checkpoint; `--list` shows them. |
| `kaku serve` | HTTP API over one conversation with SSE streaming; `--addr` sets the listen address (default `127.0.0.1:8377`). |
| `kaku mcp` | Speak MCP on stdio, exposing the agent as a `kaku` tool. |
| `kaku rpc` | Drive one conversation over a newline-delimited JSON protocol on stdin/stdout, the surface an editor embeds. See [RPC mode](#rpc-mode). |

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
| `--dangerously-skip-permissions` | Allow every tool without prompting. A loud alias for `--mode auto`. |
| `-c, --continue` | Continue the newest session in this project. `--resume` is a kept alias. |
| `--session <id>` | Continue a specific session. |
| `--fork <id>` | Copy a session into a new one and continue from the copy. The original is left untouched. The copy records the source as its parent, so `kaku sessions tree` shows the lineage. |
| `--no-session` | Run without reading or writing a session file: nothing is persisted. |
| `--title <str>` | Set the session title up front instead of deriving it from the first prompt. |
| `--output-format <text\|json>` | Headless output format. `text` (default) is human lines; `json` emits one event object per line. |
| `--json` | Shorthand for `--output-format json`. |
| `--output-schema <path>` | Constrain the answer to a JSON object that matches the JSON Schema in this file. See [Structured output](#structured-output). |
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

## Structured output

`--output-schema <path>` points at a file holding a JSON Schema and constrains the final answer to a JSON object that matches it.
This is the reliable way to get machine-parseable output out of a headless run: instead of parsing prose, you read one JSON object.

```bash
echo '{"type":"object","properties":{"files":{"type":"array","items":{"type":"string"}}},"required":["files"],"additionalProperties":false}' > schema.json
kaku -p --output-schema schema.json "list the go files in this directory"
```

OpenAI and Responses providers pass the schema to the native structured-output field with strict validation.
Anthropic has no such field, so kaku folds the schema into the system prompt as a best-effort constraint.
Pair it with `--json` when you want both the event stream and a schema-constrained `result`.

## RPC mode

`kaku rpc` drives one long-lived conversation over a newline-delimited JSON protocol
on stdin and stdout. It is the surface an editor or plugin embeds: unlike a headless
run, it stays up across many prompts, keeps the conversation in memory, and round-trips
permission prompts back to the caller instead of degrading them to a deny.

Send one command object per line on stdin. Read one object per line on stdout. The
first output line is always a `ready` line carrying the model, mode, and working
directory. All the shared flags (`--model`, `--mode`, `-C`, and so on) configure the
agent the same way they configure a headless run.

Commands:

| Command | Effect |
|---|---|
| `{"type":"prompt","id":1,"text":"..."}` | Run the agent on the text. Streams events, then a `response` echoing the id with the final `text` and `usage`. |
| `{"type":"abort","id":2}` | Cancel the running prompt. |
| `{"type":"new_session","id":3}` | Reset the conversation to a fresh session. |
| `{"type":"get_messages","id":4}` | Return the full conversation as `messages`. |
| `{"type":"set_model","id":5,"model":"x"}` | Switch the active model. |
| `{"type":"get_state","id":6}` | Return the current `model`, `mode`, `cwd`, and message count. |
| `{"type":"permission_response","id":7,"allow":true,"always":false}` | Answer a pending `permission_request` (the `id` matches the request). |

While a prompt runs, the server emits the same event shapes as the headless JSON mode
(`text`, `thinking`, `tool_start`, `tool_end`, `turn`, `info`). When a tool needs
approval it emits a `permission_request` with its own `id` and blocks until you send a
matching `permission_response`; an `abort` in the meantime denies it. A command that
fails produces an `error` line carrying the command id.

```
{"type":"ready","model":"claude-sonnet-5","mode":"ask","cwd":"/work"}
> {"type":"prompt","id":1,"text":"delete build/ then say done"}
{"type":"permission_request","id":1,"tool":"bash","arg":"rm -rf build/"}
> {"type":"permission_response","id":1,"allow":true}
{"type":"tool_start","tool":"bash","input":{"command":"rm -rf build/"}}
{"type":"tool_end","tool":"bash","output":""}
{"type":"text","text":"done"}
{"type":"turn","input_tokens":1400,"output_tokens":12}
{"type":"response","id":1,"text":"done","usage":{"input_tokens":1400,"output_tokens":12}}
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

Images ride along as image blocks rather than inlined text. An `@path` that points at
a `.png`, `.jpg`, `.jpeg`, `.gif`, or `.webp` file attaches the image (`what is @shot.png`),
and `ctrl+v` attaches an image from the system clipboard (macOS needs `pngpaste`, Linux
uses `wl-paste` or `xclip`). Attachments show as `[img: name]` chips above the composer
and clear on send. Large PNG and JPEG images are downscaled to fit 1568px on the long edge
before sending. A model without vision support will ignore the image blocks.

`ctrl+g` hands the current draft to your external editor (`$VISUAL`, then `$EDITOR`,
falling back to `vi`) so you can compose a long message in a real editing buffer. Save
and quit the editor and the edited text loads back into the composer, ready to send.

The composer action keys (`ctrl+n`, `shift+tab`, `ctrl+v`, `ctrl+g`) can be remapped
with the `keybinds` config block; see the configuration reference.

Dialogs (help, the model picker, errors, and permission prompts) open centered over
the transcript. A read-and-dismiss dialog closes on `esc` or `enter`; the picker
takes the arrow keys.

At a permission prompt: `y` allows once, `a` allows that tool for the session, `n` denies.
`esc` interrupts a running turn.

The header prints a one-line summary of what the session loaded: the number of skills,
subagents, MCP servers, and memory files. The footer shows the model, mode, thinking
level, and token counts; when the active model has a configured `cost`, it also shows a
running spend estimate.

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
