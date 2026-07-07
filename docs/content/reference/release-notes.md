---
title: "Release notes"
description: "What shipped in each kaku release."
weight: 30
---

## v0.2.0 (2026-07-07)

A developer-experience release. The engine and its guarantees are unchanged; what grew is the day-to-day surface: how you pick models, drive the TUI, embed kaku in an editor, and see what a change broke. Everything here is additive and defaults to the v0.1.0 behavior, so existing config and scripts keep working.

**Models and providers**

- Named providers in config and full model resolution: `provider/model`, a bare `model`, or `model:level`.
- Reasoning wired end to end through the provider registry, with a global `reasoning` default and a `--thinking` override.
- `--output-schema` constrains a headless answer to a JSON object that matches a JSON Schema, native on OpenAI and Responses and folded into the prompt on Anthropic.

**Run modes**

- Headless JSON mode (`--json`): one event object per line, ending in a `result`, so a program can drive the agent.
- Session flags: `--fork`, `--session`, `--no-session`, `--title`, and `-c`/`--continue`.
- `kaku rpc`: a long-lived newline-delimited JSON protocol on stdin/stdout for editors to embed, with permission prompts that round-trip instead of degrading to deny.

**TUI**

- Dialogs for errors, help, and model switching; a live thinking toggle and model cycling.
- An `@file` fuzzy picker, the `!cmd` shell prefix, `/init`, and a `/sessions` picker.
- Color themes, builtin and custom, switchable live with `/theme`.
- Image attachments from `@path` and the clipboard, sent as image blocks.
- Open the composer in `$EDITOR` with `ctrl+g`, and remap the composer action keys with the `keybinds` config block.
- A resource summary in the header and a running cost estimate in the footer.

**Sessions**

- Fork, rename, delete, and export (markdown, HTML, JSON).
- `kaku sessions tree` shows the fork lineage; a fork now records its parent.
- `kaku sessions share` writes a self-contained HTML copy you can hand off.

**Tools and editing**

- Formatters run after a write or edit, matched by extension, so the model reads canonical files.
- Optional LSP diagnostics: with `lsp` on, the write and edit tools open the touched file in a language server (gopls, pyright, rust-analyzer, typescript, clangd) and attach its diagnostics to the result.
- Tool gating by name glob with `--tools`/`--exclude-tools` and a `tools` config map.

**Permissions and extensibility**

- Permission categories and per-agent permission rules.
- A credential store: `kaku auth login/list/logout` keeps provider keys in `~/.kaku/auth.json` (0600).
- Command interpolation (`$ARGUMENTS`, backtick commands, `@file`) and instruction-file globs.

The full changelog is on the [releases page](https://github.com/tamnd/kaku/releases).

## v0.1.0 (2026-07-06)

The first release.
One binary carries the whole agent:

- **Engine**: streaming tool loop with parallel execution of consecutive read-only calls, subagent fan-out, and automatic history compaction on a token budget.
- **Providers**: Anthropic Messages, OpenAI chat completions, and the OpenAI Responses API, all streaming.
- **Builtin tools**: read, write, edit, bash, grep, glob, ls, fetch.
- **Permissions**: plan, ask, and auto modes with allow and deny rules; hooks can veto any tool call.
- **Sandbox**: `--sandbox` confines bash writes to the working directory via Seatbelt on macOS and landlock on Linux.
- **Checkpoints**: automatic pre-mutation snapshots under a hidden git ref, restored with `kaku rewind`.
- **Sessions**: append-as-you-go JSONL, `--resume`, `kaku sessions`.
- **Skills, subagents, memory**: Markdown in `.kaku/skills/`, `.kaku/agents/`, and `KAKU.md` (with `AGENTS.md`/`CLAUDE.md` fallback).
- **MCP both ways**: client over stdio and streamable HTTP; `kaku mcp` exposes the agent as a server.
- **Surfaces**: interactive TUI, headless `-p`, `kaku serve` with SSE streaming, and `pkg/engine` as a Go library.

Install channels live from this tag: Homebrew, Scoop, apt, dnf, Docker (GHCR), and signed release archives.
The full changelog is on the [releases page](https://github.com/tamnd/kaku/releases).
