---
name: agentsfs-setup
description: Set up agentsfs persistent memory — use `afs setup` for the normal create-and-connect flow, or `afs init` / `afs connect` for lower-level control, then seed starter knowledge. Use when the user wants to set up agentsfs, create persistent memory for their agents, or point a project at an existing agentsfs.
---

# Set up agentsfs memory

agentsfs is a portable, user-owned memory: a plain git repo of markdown + conventions that any agent can read and write. Your job is to get the user from nothing (or from an existing agentsfs) to a working, connected, seeded instance.

## 1. Check the tooling and location

Run:

```sh
afs version
```

If it works, continue.

If it fails and you are inside the agentsfs source repo, install it:

```sh
go install ./cmd/afs
export PATH="$(go env GOPATH)/bin:$PATH"
afs version
```

If it fails and you are not inside the source repo, ask the user where the repo is or ask them to install `afs`. The substrate still works without the CLI, but setup is much easier with it.

## 2. Choose the shape

Recommend the personal shape: one instance outside any codebase (e.g. `~/agentsfs`), with projects connected to it. Knowledge then outlives projects, never enters a codebase's git history, and is shared across everything the user does. Only choose an in-repo instance if the user explicitly wants team-shared memory committed with their code.

## 3. Create and connect

Use the command that matches the user's intent:

```sh
cd /path/to/user-project
afs setup --yes
```

Normal path. Creates or reuses `~/agentsfs` and connects the current project.

```sh
afs connect <path> --yes
```

Existing agentsfs, new project. Appends a connection block to the project's `AGENTS.md`/`CLAUDE.md`, or creates `./AGENTS.md` if absent.

```sh
afs init <path>
```

Create-only. Makes an agentsfs exactly at `<path>` and does not connect the current project.

```sh
afs init ./agentsfs --shared
```

Team-shared memory committed with the current repo. Use only after the user explicitly chooses this.

```sh
afs connect <path> --global
```

Global harness connection. Use only after the user explicitly says all future sessions for that harness should know about this agentsfs.

If the user's harness sandboxes file access to the working directory, tell them to allowlist the agentsfs path (e.g. Claude Code's permission settings) so sessions don't prompt on every read.

## 4. Seed it (the first session)

1. Read the agentsfs root `AGENTS.md` in full — it is the contract; follow it exactly.
2. Interview the user briefly: what is this memory for? Which people, organizations, projects recur? What should a future session never have to ask again?
3. Create the first structure — directories with `INDEX.md` files — and seed dense starter notes: entity pages for key people/orgs, current state of play, open questions.
4. Commit from the agentsfs root: `git status --short`, then `git add -A . && git commit`.
5. Tell the user what you stored and where. Keep it small and dense: a few well-described files beat many stubs.

If git identity is missing, explain that the commit failed and leave the files ready for the user to commit.
