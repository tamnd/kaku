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
| `reasoning` | Global default reasoning level: `off`, `minimal`, `low`, `medium`, `high`, or `xhigh`. A per-model setting or the `--thinking` flag overrides it. |
| `theme` | TUI color theme. Builtins are `dark` (default) and `light`; custom themes load from `~/.kaku/themes/*.json` and `.kaku/themes/*.json`. Switch live with `/theme`. |
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

## Rule syntax

A rule is a tool name, optionally with a glob on the call's primary argument: the command for `bash`, the path for file tools, the URL for `fetch`.

```
"bash"                  every bash call
"bash(go test *)"       bash commands starting with "go test "
"write(docs/*)"         writes under docs/
"mcp__docs__search"     one MCP tool
"*"                     every tool
```

## Project instructions

Not configuration, but read on every run: `KAKU.md` at the repo root is added to the system prompt, and `AGENTS.md` or `CLAUDE.md` are used as fallbacks, in that order.

To pull in more files, list globs under `instructions`. They resolve relative to the project root and their contents are appended after the walked instruction files and the `.kaku/memory/*.md` facts. Everything shares one 48KB budget; once it fills, the rest are dropped with a note.
