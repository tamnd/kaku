---
title: "Quick start"
description: "From an empty terminal to a reviewed, checkpointed change in five minutes."
weight: 30
---

## 1. Connect a model

kaku defaults to the Anthropic API:

```bash
export ANTHROPIC_API_KEY=...
```

Or point it at any OpenAI-compatible endpoint, local or hosted:

```bash
kaku --provider openai --base-url http://127.0.0.1:8080/v1 --model qwen3-30b
```

Flags win over config, so you can keep a default in `~/.kaku/config.json` and override per run.

## 2. Talk to your project

```bash
cd your-project
kaku
```

The TUI opens a fresh session.
Ask for something real:

> the tests in pkg/parser fail on go 1.26, find out why and fix it

kaku reads files, greps, runs the tests, and streams every tool call as it happens.
In the default `ask` mode it stops before each file edit or command and asks; answer `y` for once, `a` for always this session, `n` to deny.

Useful keys and commands inside the TUI:

- `/model` shows or switches the model mid-session.
- `/compact` summarizes old turns when the conversation gets long.
- `/skills` lists the slash commands your skills define.
- `esc` interrupts a running turn.

## 3. Run headless

For scripts and CI, `-p` prints the answer and exits:

```bash
kaku -p "summarize what changed in the last commit"
git diff | kaku -p "review this diff"
```

Piped stdin becomes part of the prompt.
Headless runs cannot prompt for permission, so ask mode denies mutating tools; pass `--mode auto` when you mean it.

## 4. Trust, then verify

Everything kaku did this session is on disk:

```bash
kaku sessions           # list this project's sessions
kaku --resume           # continue the newest one
kaku rewind --list      # checkpoints taken before each mutating turn
kaku rewind             # put the tree back to the newest checkpoint
```

Rewinds are themselves checkpointed, so nothing is one-way.

## Next

- Lock down what the agent may do in [permissions and the sandbox](/guides/permissions-and-sandbox/).
- Teach it your project's habits with [skills, subagents, and memory](/guides/skills-agents-memory/).
- Wire in external tools or expose kaku itself over [MCP](/guides/mcp/).
