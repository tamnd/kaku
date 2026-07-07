---
title: "Configuration"
description: "Every key in config.json and settings.json, and the order they merge in."
weight: 20
---

Configuration merges in three layers, later wins:

1. Built-in defaults.
2. `~/.kaku/config.json` (global).
3. `.kaku/settings.json` in the project.

Command-line flags override all three for that run.

## All keys

```json
{
  "provider": "anthropic",
  "model": "claude-sonnet-5",
  "small_model": "claude-haiku-4-5",
  "base_url": "",
  "api_key_env": "ANTHROPIC_API_KEY",
  "max_tokens": 0,
  "max_turns": 0,

  "permissions": {
    "mode": "ask",
    "allow": ["bash(go test *)"],
    "deny": ["bash(rm -rf *)"]
  },

  "mcpServers": {
    "docs": {"command": "docs-mcp", "args": ["--root", "./docs"], "env": {"TOKEN": "..."}},
    "tracker": {"url": "https://tracker.example.com/mcp"}
  },

  "hooks": {
    "pre_tool": [{"match": "bash", "command": "./scripts/check.sh"}]
  },

  "instructions": ["CONTRIBUTING.md", "docs/conventions/*.md"],

  "tools": {"fetch": false, "mcp__*": true},

  "formatter": true,

  "reasoning": "medium",

  "providers": {
    "zen": {
      "api": "openai",
      "base_url": "https://opencode.ai/zen/v1",
      "api_key": "{env:OPENCODE_API_KEY}",
      "models": {
        "big-pickle": {"reasoning": "medium", "max_tokens": 32000}
      }
    }
  }
}
```

| Key | Meaning |
|---|---|
| `provider` | `anthropic`, `openai`, or `responses`. |
| `model` | The main model. |
| `small_model` | Cheap model for compaction summaries and other utility calls; falls back to `model`. |
| `base_url` | API endpoint override, for local servers and proxies. |
| `api_key_env` | Name of the environment variable holding the key. Switching provider by flag also switches the default (`ANTHROPIC_API_KEY` or `OPENAI_API_KEY`). |
| `max_tokens` | Response token cap per model call. |
| `max_turns` | Cap on model turns per run. |
| `permissions.mode` | `plan`, `ask`, or `auto`. |
| `permissions.allow`, `permissions.deny` | Rules in `tool` or `tool(arg-glob)` form; deny wins over allow, allow wins over the mode. |
| `mcpServers.<name>.command/args/env` | Spawn this MCP server over stdio. |
| `mcpServers.<name>.url` | Dial this MCP server over streamable HTTP. |
| `hooks.<event>` | Commands to run on `pre_tool`, `post_tool`, `user_prompt`, or `stop`; `match` is a glob on the tool name, exit 2 blocks the action. |
| `instructions` | Extra instruction-file globs, resolved relative to the project root, appended to the system prompt after `KAKU.md` and the memory files. |
| `tools.<glob>` | Enable or disable tools by name glob. `false` removes the tool from the registry so the model never sees it; the `--tools`/`--exclude-tools` flags override this. |
| `formatter` | Format files after a write or edit. `true` enables the builtins, `false` (default) is off. An object enables them and tweaks: see below. |
| `lsp` | Attach language-server diagnostics after a write or edit. `true` enables the builtins, `false` (default) is off. An object enables them and tweaks: see below. |
| `reasoning` | Global default reasoning level: `off`, `minimal`, `low`, `medium`, `high`, or `xhigh`. A per-model setting or the `--thinking` flag overrides it. |
| `theme` | TUI color theme. Builtins are `dark` (default) and `light`; custom themes load from `~/.kaku/themes/*.json` and `.kaku/themes/*.json`. Switch live with `/theme`. |
| `keybinds` | Override TUI composer keys by action name. See [Keybinds](#keybinds). |
| `providers.<name>` | A named custom provider: its wire format, endpoint, credential, and models. See below. |

## Named providers

The flat `provider`/`model`/`base_url`/`api_key_env` fields describe one default provider, and that keeps working untouched.
To register more, add entries under `providers`. Each names a wire format (`api`), an endpoint, a credential, and the models it serves.

```json
"providers": {
  "zen": {
    "api": "openai",
    "base_url": "https://opencode.ai/zen/v1",
    "api_key": "{env:OPENCODE_API_KEY}",
    "headers": {"X-Title": "kaku"},
    "models": {
      "big-pickle": {"reasoning": "medium", "max_tokens": 32000, "context": 200000, "cost": {"input": 3, "output": 15}}
    }
  }
}
```

| Key | Meaning |
|---|---|
| `providers.<name>.api` | Wire format: `anthropic`, `openai`, or `responses`. |
| `providers.<name>.base_url` | The endpoint for this provider. |
| `providers.<name>.api_key` | The credential. A bare string, `{env:VAR}` to read an environment variable, or `{file:~/.secrets/zen}` to read a file's trimmed contents. A missing variable or unreadable file is a loud error. |
| `providers.<name>.headers` | Extra HTTP headers sent on every request. |
| `providers.<name>.models.<id>` | One model. `reasoning` sets its default level, `max_tokens` its response cap, `context` and `name` are metadata, and `cost` (`{input, output}` in USD per million tokens) drives the TUI footer's spend estimate. Without a `cost` the footer shows tokens only. |

Reference a model as `provider/model` (`zen/big-pickle`) or, when the name is unique across all providers, bare (`big-pickle`).
A `:level` suffix sets reasoning for that run: `zen/big-pickle:high`.
A bare name that is not found in any provider map is treated as a model on the default provider, so `--model claude-opus-4-8` still works with no `providers` block at all.
Run `kaku models` to print every model kaku can resolve.

## Keybinds

`keybinds` remaps the TUI composer's action keys. Each entry is an action name mapped to a key string; unset actions keep their defaults. Core keys (`enter` to send, `ctrl+c` to quit, `esc` to interrupt, `@` for the file picker) are fixed so a bad config cannot lock you out.

```json
{
  "keybinds": {
    "editor": "ctrl+e",
    "paste_image": "ctrl+y"
  }
}
```

| Action | Default | What it does |
|---|---|---|
| `model_cycle` | `ctrl+n` | Step to the next model in the cycle list. |
| `reasoning_cycle` | `shift+tab` | Step the reasoning level. |
| `paste_image` | `ctrl+v` | Attach an image from the system clipboard. |
| `editor` | `ctrl+g` | Open the draft in `$EDITOR`. |

Key strings follow Bubble Tea's names: `ctrl+x`, `alt+enter`, `f2`, and so on.

## Credential order

For each provider kaku resolves a key in this order and uses the first that is non-empty:

1. An explicit `api_key` on a named provider (`{env:VAR}` or `{file:path}`), or the flat `api_key_env` / `--api-key-env` for the default provider.
2. A stored key in `~/.kaku/auth.json` under the provider name.
3. The provider's default environment variable (today's behavior, unchanged).

Store a key with `kaku auth login [provider]`, which reads it from the terminal without echoing and writes `~/.kaku/auth.json` with 0600 permissions.
`kaku auth list` shows which providers have a stored key without ever printing one, and `kaku auth logout [provider]` removes it.
The file holds a plain `{"anthropic": "sk-...", "zen": "..."}` map; keep it out of version control.
Because the environment variable still wins when it is set, adding a stored key changes nothing for a session that already exports its key.

## Rule syntax

A rule is a tool name, optionally with a glob on the call's primary argument: the command for `bash`, the path for file tools, the URL for `fetch`.

```
"bash"                  every bash call
"bash(go test *)"       bash commands starting with "go test "
"write(docs/*)"         writes under docs/
"mcp__docs__search"     one MCP tool
"*"                     every tool
```

A rule can also name a category, which expands to its member tools at load. Any argument glob carries over to each member.

| Category | Covers |
|---|---|
| `edit` | `edit`, `write` |
| `read` | `read`, `ls`, `glob`, `grep` |
| `bash` | `bash` |
| `webfetch` | `fetch` |

So `"deny": ["edit"]` blocks both `edit` and `write`, and `"edit(docs/*)"` gates writes under `docs/` for both.

`--dangerously-skip-permissions` is a loud alias for `--mode auto`: it allows every tool without prompting.

## Formatters on write

With `formatter` on, kaku runs a formatter over each file the `write` and `edit` tools touch, matched by extension, so the model reads canonical files and diffs stay small. It is off by default, so today's raw-write behavior is preserved until you opt in.

`"formatter": true` enables the builtins:

| Formatter | Extensions | Command |
|---|---|---|
| `gofmt` | `.go` | `gofmt -w` |
| `rustfmt` | `.rs` | `rustfmt` |
| `prettier` | `.ts`, `.tsx`, `.js`, `.jsx`, `.mjs`, `.css`, `.html`, `.json`, `.md`, `.yaml`, `.yml` | `prettier --write` |
| `ruff` | `.py` | `ruff format` |

A formatter only runs when its binary is on `PATH`; a missing one is a silent skip, and a formatter that fails leaves the file as written. To disable a builtin or add your own, use the object form:

```json
"formatter": {
  "gofmt": {"disabled": true},
  "deno": {"command": ["deno", "fmt", "$FILE"], "extensions": [".md"]}
}
```

`$FILE` is replaced with the written path. The object form still enables the other builtins.

## LSP diagnostics on write

With `lsp` on, kaku opens each file the `write` and `edit` tools touch in a matching language server and appends the diagnostics the server reports to the tool result, so the model sees type errors without running a build. Servers start on first touch and stay warm for the session. It is off by default.

`"lsp": true` enables the builtins:

| Server | Extensions | Command |
|---|---|---|
| `gopls` | `.go` | `gopls` |
| `pyright` | `.py` | `pyright-langserver --stdio` |
| `rust-analyzer` | `.rs` | `rust-analyzer` |
| `typescript` | `.ts`, `.tsx`, `.js`, `.jsx`, `.mjs`, `.cjs` | `typescript-language-server --stdio` |
| `clangd` | `.c`, `.cc`, `.cpp`, `.h`, `.hpp` | `clangd` |

A server only runs when its binary is on `PATH`; a missing one is a silent skip, and a file that opens clean adds nothing to the result. To disable a builtin or add your own, use the object form:

```json
"lsp": {
  "gopls": {"disabled": true},
  "zls": {"command": ["zls"], "extensions": [".zig"], "language_id": "zig"}
}
```

The object form still enables the other builtins.

## Project instructions

Not configuration, but read on every run: `KAKU.md` at the repo root is added to the system prompt, and `AGENTS.md` or `CLAUDE.md` are used as fallbacks, in that order.

To pull in more files, list globs under `instructions`. They resolve relative to the project root and their contents are appended after the walked instruction files and the `.kaku/memory/*.md` facts. Everything shares one 48KB budget; once it fills, the rest are dropped with a note.
