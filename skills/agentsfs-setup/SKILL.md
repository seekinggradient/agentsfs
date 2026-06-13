---
name: agentsfs-setup
description: Set up agentsfs persistent memory — create a vault with `afs init`, connect projects to it with `afs register`, and seed it with starter knowledge. Use when the user wants to set up agentsfs, create a memory vault for their agents, or point a project at an existing vault.
---

# Set up agentsfs memory

agentsfs is a portable, user-owned memory: a plain git repo of markdown + conventions that any agent can read and write. Your job is to get the user from nothing (or from an existing vault) to a working, registered, seeded instance.

## 1. Check the tooling

Run `afs version`. If missing, the substrate still works without it (plain files + git), but setup is easier with it — ask the user to install `afs` first if they can.

## 2. Choose the shape (one question)

Recommend the **personal vault**: one instance outside any codebase (e.g. `~/agentsfs`), with projects pointing at it. Knowledge then outlives projects, never enters a codebase's git history, and is shared across everything the user does. Only choose an in-repo instance if the user explicitly wants team-shared memory committed with their code.

## 3. Create and register

- New vault: `afs init ~/agentsfs` (or the user's chosen path). Approve registration prompts with the user; `--register-global` writes their global harness configs so every session everywhere knows the vault.
- Existing vault, new project: from the project directory, `afs register <vault-path>` — it appends a registration block to the project's AGENTS.md/CLAUDE.md (offers to create one if absent).
- If the user's harness sandboxes file access to the working directory, tell them to allowlist the vault path (e.g. Claude Code's permission settings) so sessions don't prompt on every read.

## 4. Seed it (the first session)

1. Read the vault's `AGENTS.md` in full — it is the contract; follow it exactly.
2. Interview the user briefly: what is this memory for? Which people, organizations, projects recur? What should a future session never have to ask again?
3. Create the first structure — directories with `INDEX.md` files — and seed dense starter notes: entity pages for key people/orgs, current state of play, open questions.
4. Commit from the vault root: `git add -A . && git commit`.
5. Tell the user what you stored and where. Keep it small and dense: a few well-described files beat many stubs.
