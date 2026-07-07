package engine

// InitPrompt drives /init and `kaku init`. It is a single agent run that scans
// the working tree and writes a starter KAKU.md, the file kaku loads as project
// instructions on later runs.
const InitPrompt = `Scan this repository and write a KAKU.md at its root that future kaku sessions will load as project instructions. Keep it short and factual, no filler.

Do the work in this order:
1. Detect the primary language and toolchain from the manifests and config you find (go.mod, package.json, Cargo.toml, pyproject.toml, Makefile, and so on).
2. Find the real build, test, lint, and run commands. Prefer what the manifests and scripts actually define over guesses.
3. Sketch the top-level layout: the directories that matter and what each holds.
4. Leave a short "Conventions" section as a placeholder for the maintainer to fill in.

Then write KAKU.md with these sections: a one-line project summary, Build and test (the commands), Layout (the directory map), and Conventions (the placeholder). Use the file tools to read what you need and to write KAKU.md. When the file is written, stop and report the path.`
