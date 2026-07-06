---
title: "kaku"
description: "Kaku (書く, to write) is a coding agent in one static Go binary. It runs against the Anthropic API or any OpenAI-compatible endpoint, keeps sessions as JSONL and skills as Markdown, and exposes the same engine as a TUI, a CLI, an HTTP server, an MCP server, and a Go package."
heroTitle: "A coding agent that lives in your terminal"
heroLead: "kaku reads your project, edits files, runs commands, and answers in a stream. One Go binary, any Anthropic or OpenAI-compatible model, everything on disk is a file you can cat."
heroPrimaryURL: "/getting-started/quick-start/"
heroPrimaryText: "Get started"
---

You point kaku at a project and talk to it.
It reads the code, edits files, runs the tests, and streams what it is doing while it does it.
There is no runtime to install and no cloud lock-in: one static binary, and the model behind it is whichever Anthropic or OpenAI-compatible endpoint you give it, including a llama.cpp or MLX server on your own hardware.

```bash
kaku                            # interactive TUI in the current project
kaku -p "fix the failing test"  # headless: answer, then exit
```

## What it does

- **Everything is a file.** Sessions are JSONL you can cat, grep, and resume. Skills, subagents, and project memory are Markdown you edit by hand.
- **Permissions are explicit.** Plan, ask, or auto mode, with allow and deny rules in settings. An optional sandbox confines bash writes to the working directory.
- **Checkpoints, not regrets.** In a git repo, kaku snapshots the tree before each file-changing turn under a hidden ref. `kaku rewind` puts things back, and a rewind can itself be undone.
- **Tools stay small and trusted.** Eight builtins (read, write, edit, bash, grep, glob, ls, fetch) plus whatever MCP servers you configure.
- **One engine, five surfaces.** The same Go package drives the TUI, headless runs, `kaku serve` over HTTP with SSE, `kaku mcp` as an MCP server, and your own programs.

## Where to go next

- New here? Start with the [introduction](/getting-started/introduction/), then the [quick start](/getting-started/quick-start/).
- Install from Homebrew, Scoop, apt or dnf, Docker, or `go install` on the [installation page](/getting-started/installation/).
- The [guides](/guides/) cover permissions, sessions and checkpoints, skills and memory, MCP in both directions, and the HTTP server.
- The [reference](/reference/) has every command, flag, and configuration key.

Sibling of [yomi](https://yomi.tamnd.com): yomi reads the web, kaku writes code.
