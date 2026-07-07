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

## Which one do I want?

- Always true about the project: `KAKU.md`.
- A prompt you keep retyping: a skill.
- A chunk of work with noisy intermediate steps: a subagent.
