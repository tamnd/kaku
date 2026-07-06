---
title: "Introduction"
description: "Why kaku keeps everything in plain files and treats the agent engine as a library."
weight: 10
---

A coding agent is a loop: send the conversation to a model, run the tools it asks for, feed the results back, repeat until it answers.
kaku implements that loop once, as a plain Go package, and everything else is a thin adapter over it.
The interactive TUI, the headless `-p` mode, the HTTP server, and the MCP server are all the same engine wearing different clothes.

Two convictions shape the rest.

## Everything is a file

State you cannot inspect is state you cannot trust.
So kaku keeps its state in formats you already have tools for:

- **Sessions** are JSONL under `~/.kaku/sessions/<project>/`. `cat` one to read a conversation, `grep` across them, pass `--resume` to continue the newest.
- **Skills** are Markdown files in `.kaku/skills/` (project) or `~/.kaku/skills/` (user). A skill named `review.md` becomes the `/review` command.
- **Subagents** are Markdown files in `.kaku/agents/`, each defining a system prompt the main loop can fan work out to.
- **Project memory** is `KAKU.md` at the repo root. `AGENTS.md` and `CLAUDE.md` are picked up too, so kaku drops into repos already set up for other agents.
- **Configuration** is JSON: global in `~/.kaku/config.json`, per-project in `.kaku/settings.json`.

Nothing is hidden in a database, and every file is small enough to edit by hand.

## The model is a flag

kaku speaks three provider protocols: the Anthropic Messages API, OpenAI chat completions, and the OpenAI Responses API.
All three stream.
Which one you use is a flag or a config key, not a build decision:

```bash
kaku                                                            # Anthropic, from ANTHROPIC_API_KEY
kaku --provider openai --base-url http://127.0.0.1:8080/v1 \
     --model qwen3-30b                                          # local llama.cpp or MLX server
kaku --provider responses --base-url http://127.0.0.1:8000/v1 \
     --model gpt-5                                              # anything speaking the Responses API
```

The engine does not care where the endpoint lives.
A hosted API, a proxy, and a model on your own GPU are all the same three lines of config.

## What kaku is not

kaku does not phone home, does not require an account, and does not bundle a browser.
External capability arrives over MCP, where you decide which servers to connect.
The builtin toolset stays small enough to audit in an afternoon: read, write, edit, bash, grep, glob, ls, fetch.

Next: the [quick start](/getting-started/quick-start/).
