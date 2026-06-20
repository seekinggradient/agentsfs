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
afs help | grep "afs setup"
```

If both commands work, continue. If `afs` is missing or too old to show `afs setup`, install or update it.

Prefer installing into `~/.local/bin` for agent-run setup. Agent shells often inherit that path but do not read interactive shell profiles that add `~/go/bin`.

If the user has a local checkout, set `AGENTSFS_SOURCE` and install from there:

```sh
export PATH="$HOME/.local/bin:$PATH"
mkdir -p "$HOME/.local/bin"
export AGENTSFS_SOURCE=/path/to/agentsfs
(cd "$AGENTSFS_SOURCE" && GOBIN="$HOME/.local/bin" go install ./cmd/afs)
hash -r 2>/dev/null || true
command -v afs
afs version
```

Otherwise, try the packaged installer:

```sh
export PATH="$HOME/.local/bin:$PATH"
mkdir -p "$HOME/.local/bin"
AFS_INSTALL_DIR="$HOME/.local/bin" curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | sh
hash -r 2>/dev/null || true
command -v afs
afs version
```

If the installer gets a 404 from GitHub, the repo or release assets are not public yet. Ask the user for a local checkout path and use the `AGENTSFS_SOURCE` flow above.

If the installer cannot download a release asset and cannot build from source, ask the user to install Go and git.

If you are inside the agentsfs source repo, this also works:

```sh
export PATH="$HOME/.local/bin:$PATH"
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" go install ./cmd/afs
hash -r 2>/dev/null || true
command -v afs
afs version
```

Do not treat setup as complete until `command -v afs` and `afs version` work in the current agent shell. If all install paths fail, ask the user to install `afs`. The substrate still works without the CLI, but setup is much easier with it.

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
2. Interview the user briefly for domain context, not taxonomy: what is this memory for? Which people, organizations, projects, documents, systems, and decisions recur? What should a future session never have to ask again?
3. Choose the first structure yourself — directories with `INDEX.md` files — and seed dense starter notes: entity pages for key people/orgs, current state of play, open questions. Do not ask the user how to structure the knowledge base; make a reasonable structure, explain it briefly, and reorganize later as the memory grows.
4. Commit from the agentsfs root: `git status --short`, then `git add -A . && git commit`.
5. Tell the user what you stored and where. Keep it small and dense: a few well-described files beat many stubs.

If git identity is missing, explain that the commit failed and leave the files ready for the user to commit.

## 5. Offer ordinary Git/GitHub backup

agentsfs has no managed hosting layer. If the user wants backup or cross-device sync, recommend a private GitHub repository, GitLab repository, or self-hosted git remote.

Ask about the user's goal before introducing Git. Use this order:

- Do you want this agentsfs backed up or synced across computers?
- Do you know what Git is?
- Do you have a GitHub account?

If the user wants GitHub sync, guide them through creating an empty private repo and then connect it from the agentsfs root:

```sh
git remote add origin git@github.com:<user>/<repo>.git
git branch -M main
git push -u origin main
```

If they are new to Git, explain that Git records local history and GitHub can keep a private online copy. Never store GitHub credentials, personal access tokens, or SSH private keys in the agentsfs repo.
