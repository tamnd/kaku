---
title: "Release notes"
description: "What shipped in each kaku release."
weight: 30
---

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
