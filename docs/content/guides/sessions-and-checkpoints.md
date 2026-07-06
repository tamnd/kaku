---
title: "Sessions and checkpoints"
description: "Conversations as JSONL you can resume, and git-backed snapshots you can rewind."
weight: 20
---

kaku separates what was said from what was changed.
Sessions record the conversation; checkpoints record the working tree.
Both live in plain, inspectable places.

## Sessions

Every conversation is appended to a JSONL file under `~/.kaku/sessions/<project>/`, one JSON message per line as it happens.
Kill the terminal mid-turn and nothing before that turn is lost.

```bash
kaku sessions           # list this project's sessions, newest first
kaku --resume           # continue the newest
kaku --session <id>     # continue a specific one
```

Because a session is just a file, the usual tools work:

```bash
cat ~/.kaku/sessions/*/2026*.jsonl | jq -r 'select(.role=="user")'
grep -rl "TODO" ~/.kaku/sessions/
```

Long conversations compact automatically: when the history passes a token budget, older turns are summarized into one message and recent turns are kept verbatim.
`/compact` in the TUI forces this early.

## Checkpoints

In a git repository, kaku snapshots the working tree before the first file-changing tool call of every turn.
Snapshots are real git commits, but they live under a hidden ref (`refs/kaku/checkpoint`), so your branches, index, and HEAD are never touched, and `git log` stays clean.
Untracked files are included; ignored files are not, so build output and caches survive a rewind.

```bash
kaku rewind --list      # every checkpoint with time and label
kaku rewind             # restore the newest
kaku rewind 8602b78     # restore a specific one
```

A rewind first checkpoints the current state, so rewinding is never one-way: rewind, look around, rewind back.

Checkpoints need nothing from you.
There is no flag to remember; if the directory is a git repo, they happen.
Outside a git repo the feature quietly stays off.

## How they fit together

A typical recovery looks like:

```bash
kaku rewind --list      # find the checkpoint before the bad turn
kaku rewind <sha>       # put the files back
kaku --resume           # pick the conversation back up and steer it differently
```

The conversation still remembers the failed attempt, which is often exactly what you want: "that approach broke the build, try the other one".
