---
title: "Skills, subagents, and memory"
description: "Teach kaku your project's habits with Markdown files it reads on startup."
weight: 30
---

Three kinds of Markdown teach kaku how your project works.
None of them require a restart to edit, and all of them belong in version control.

## Project memory: KAKU.md

`KAKU.md` at the repo root is prepended to the system prompt of every run in that project.
Put in it what you would tell a new teammate on day one:

```markdown
# myproject

- Run tests with `make test`, not `go test ./...`; the Makefile sets build tags.
- The public API lives in api/; everything else is free to change.
- Never edit generated files under gen/.
```

kaku also reads `AGENTS.md` and `CLAUDE.md` if present, so a repo already set up for other coding agents works unchanged.

## Skills: reusable prompts as slash commands

A skill is a Markdown file whose name becomes a command.
Project skills live in `.kaku/skills/`, personal ones in `~/.kaku/skills/`; on a name clash the project wins.

```markdown
<!-- .kaku/skills/review.md -->
---
description: Review a diff against our standards
---
Review the following change like a careful senior engineer.
Check error handling, test coverage, and naming.
Diff or files to review: $ARGUMENTS
```

Then, in the TUI or headless:

```bash
/review pkg/parser
kaku -p "/review $(git diff --name-only HEAD~1 | tr '\n' ' ')"
```

`$ARGUMENTS` is replaced by whatever follows the command; a skill without it gets the arguments appended under an `Arguments:` label.

### Interpolation

A skill body can pull in more than the raw argument string:

| Syntax | Expands to |
|---|---|
| `$ARGUMENTS`, `$@` | Every argument, verbatim. |
| `$1`, `$2`, ... | The Nth positional argument. Quoting groups a phrase into one argument, so `"fix login" now` makes `$1` be `fix login`. |
| `${1:-default}` | The Nth argument, or `default` when it is missing or empty. |
| `` !`command` `` | The command runs in a shell rooted at the project and its output is substituted in place. It times out after 30 seconds and its output is capped. |
| `@path` | The file's contents, inlined in a `<file path="...">` block. |

```markdown
<!-- .kaku/skills/fixup.md -->
---
description: Draft a fix for an issue on the current branch
---
We are on branch !`git branch --show-current`.
The failing test output:
@testresults.txt

Fix the ${1:-first} problem you see.
```

`@path` mentions also work in a plain prompt, no skill required: `kaku -p "explain @main.go"` inlines the file before the model sees it. Paths resolve relative to the project root (or absolute, or `~`-rooted); a mention that does not point at a readable file is left as written, so an email address survives. Each file is capped at 100KB and the whole prompt at 256KB.

### Targeting a model or agent

Skill frontmatter can pin where the command runs. `model` switches the active model for the command, and `agent` borrows a subagent's model override, so a review command lands on the reviewer's model in one keystroke:

```markdown
<!-- .kaku/skills/review.md -->
---
description: Review a diff on the reviewer model
agent: reviewer
---
Review @$1 for correctness and tests.
```

A reference that does not resolve leaves the current model in place rather than failing the command.

## Subagents: fan work out

A subagent is a Markdown file in `.kaku/agents/` defining a specialist the main loop can delegate to through its `agent` tool:

```markdown
<!-- .kaku/agents/tester.md -->
---
description: Runs and fixes tests, reports results
---
You are a test specialist.
Run the relevant tests, diagnose failures, and report concisely.
```

The model decides when to use one, the same way it decides to grep.
Each delegation runs in its own context with the same toolset, and subagents never get the `agent` tool back, so fan-out stays one level deep.
Subagents keep the main conversation small: the specialist burns its own context on the noisy work and returns only the conclusion.

The frontmatter can also pin a `model`, an allowlist of `tools`, and a `permission` block that tightens what the subagent may do:

```markdown
<!-- .kaku/agents/reviewer.md -->
---
description: Reviews a diff, never edits
model: claude-haiku-4-5
tools: read, grep, glob, ls
permission:
  edit: deny
---
You review code and report findings. Do not modify files.
```

Keys under `permission` name a tool or a category (`edit`, `read`, `bash`, `webfetch`) and map it to `allow`, `ask`, or `deny`. A subagent inherits the parent's rules with its own layered on top, so `edit: deny` denies both `edit` and `write` even when the parent runs in `--mode auto`. Since a subagent has nobody to prompt, `ask` acts as a deny.

The full frontmatter field set:

| Field | Effect |
|---|---|
| `description` | What the agent is for. The main model reads it to decide when to delegate. |
| `model` | Model override for this agent's runs. Defaults to the parent's model. |
| `tools` | Comma-separated allowlist. The agent only sees these tools; it never gets `agent` back. |
| `permission` | Per-tool or per-category `allow`/`ask`/`deny` block, layered over the inherited rules. |
| `temperature`, `top_p` | Sampling knobs passed to the provider. Left unset by default. |
| `steps` | Turn cap for this agent's runs, overriding the parent's `max_turns`. |
| `hidden` | Keep the agent out of the delegation list so the main model cannot pick it. Use it for helpers a command invokes by name with `agent:`. |

## Which one do I want?

- Always true about the project: `KAKU.md`.
- A prompt you keep retyping: a skill.
- A chunk of work with noisy intermediate steps: a subagent.
