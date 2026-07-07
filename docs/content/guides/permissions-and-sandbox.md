---
title: "Permissions and the sandbox"
description: "Plan, ask, and auto modes, allow and deny rules, and the optional write sandbox for bash."
weight: 10
---

An agent that edits files and runs commands needs a leash you actually control.
kaku gives you three layers: a mode, rules, and an optional kernel-level sandbox.

## Modes

- **plan**: read-only. The agent can read, grep, glob, ls, and fetch, but every mutating tool is denied. Good for "look around and propose".
- **ask** (default): read-only tools run freely; anything that writes or executes stops and asks you first.
- **auto**: everything runs. Use it when you trust the task or you are watching closely.

Set the mode per run with `--mode`, per project in `.kaku/settings.json`, or globally in `~/.kaku/config.json`:

```json
{"permissions": {"mode": "ask"}}
```

At an ask prompt, `y` allows once, `a` allows that tool for the rest of the session, and `n` denies.
Headless runs (`-p`, `kaku serve`, `kaku mcp`) have nobody to ask, so ask mode degrades to deny there.

## Allow and deny rules

Rules cut through the mode for specific calls.
Each rule is a tool name with an optional argument pattern:

```json
{
  "permissions": {
    "mode": "ask",
    "allow": ["bash(go test *)", "bash(go build *)", "write"],
    "deny":  ["bash(rm *)", "fetch"]
  }
}
```

Deny always wins, then allow, then the mode decides what is left.
This is how you land on a comfortable middle: ask mode overall, but the commands you run twenty times a day pre-approved.

A rule can name a category instead of a single tool, which saves listing every member. `edit` covers `edit` and `write`, `read` covers `read`, `ls`, `glob`, and `grep`, `bash` is itself, and `webfetch` is `fetch`. So `"deny": ["edit"]` blocks all file writes in one line. Any argument glob applies to each member, so `"allow": ["edit(docs/*)"]` pre-approves writes under `docs/`.

Subagents defined in `.kaku/agents/` can carry their own `permission` block that layers on top of the inherited rules, so a reviewer agent can be denied edits even when the main run is in auto mode. See the subagents guide.

`--dangerously-skip-permissions` is a loud shortcut for `--mode auto`: every tool runs without a prompt. Use it only when you already trust the run.

## The sandbox

Rules control which calls happen; the sandbox controls what a call can touch once it runs.

```bash
kaku --sandbox
```

With `--sandbox`, every bash command is confined so file writes only work inside the working directory and temp locations.
Reads and network stay open, so builds, tests, and package downloads keep working.

- On macOS this uses Seatbelt (`sandbox-exec`) with a deny-writes profile.
- On Linux it uses landlock, applied in the child process just before bash starts.
- On Linux kernels without landlock the flag degrades to no confinement, and on other platforms it is unsupported.

The sandbox is per-command and adds no daemon and no container.
It composes with the modes: `--mode auto --sandbox` is "run freely, but only in this directory", a good default for unattended runs.

## Hooks as a veto

For policy that rules cannot express, hooks run your own scripts around the loop.
A `pre_tool` hook sees every tool call before it runs and can block it:

```json
{
  "hooks": {
    "pre_tool": [{"match": "bash", "command": "./scripts/check-command.sh"}]
  }
}
```

The hook gets the call as JSON on stdin.
Exit 0 lets it pass, exit 2 blocks it and turns the hook's stderr into the error the model sees, and any other failure becomes a warning.
`post_tool`, `user_prompt`, and `stop` events exist too.
