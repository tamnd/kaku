---
title: "MCP in both directions"
description: "Connect external tool servers to kaku, and expose kaku itself as an MCP server other agents can call."
weight: 40
---

The Model Context Protocol is how kaku talks to the outside world, in both directions.
As a client, kaku connects to MCP servers and their tools join the toolset.
As a server, `kaku mcp` makes the whole agent one tool another agent can call.

## kaku as a client

Declare servers in `~/.kaku/config.json` or per project in `.kaku/settings.json`:

```json
{
  "mcpServers": {
    "docs":    {"command": "docs-mcp", "args": ["--root", "./docs"]},
    "tracker": {"url": "https://tracker.example.com/mcp"}
  }
}
```

A `command` entry is spawned as a child process speaking MCP over stdio.
A `url` entry is dialed over streamable HTTP.
Either way, the server's tools register under `mcp__<server>__<tool>` and the model uses them like any builtin.

Connection failures do not kill the run.
A server that will not start is reported once and skipped, and `--no-mcp` skips connecting entirely for a fast start.

Permissions apply to MCP tools like everything else: they are treated as mutating, so ask mode prompts for them, and rules can target them by name:

```json
{"permissions": {"allow": ["mcp__docs__search"]}}
```

## kaku as a server

The other direction is one command:

```bash
kaku mcp
```

This speaks MCP on stdio and advertises a single `kaku` tool that takes a prompt and runs it through the full agent: tools, permissions, skills, checkpoints, everything.
Add it to another agent's MCP configuration:

```json
{
  "mcpServers": {
    "kaku": {"command": "kaku", "args": ["mcp", "-C", "/path/to/project", "--mode", "auto"]}
  }
}
```

Now that agent can delegate whole coding tasks: "ask kaku to fix the failing tests in that repo".
Calls share one conversation for the life of the process, so a follow-up call remembers what the previous one did.

Two things to know:

- The server is headless, so ask-mode prompts degrade to deny. Give it `--mode auto`, or allow rules for what it should be able to do.
- `-C` pins the project directory, so the calling agent cannot wander the filesystem beyond the tools' own reach.

## Why both directions matter

Client-side MCP keeps the builtin toolset small: capability you need arrives as a server you chose, not as another builtin to audit.
Server-side MCP makes kaku composable: an orchestrating agent can treat a whole project-aware coding agent as one well-typed tool.
