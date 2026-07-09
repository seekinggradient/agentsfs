---
description: Agent-facing primer for understanding, setting up, and using AgentsFS from a fresh workspace.
---

# AgentsFS agent-start

Use this when a user wants an AI agent to understand AgentsFS, set it up, or connect a new project to an existing AgentsFS.

## What AgentsFS is

AgentsFS is a durable, local-first knowledge base for AI agents. It gives Claude, Codex, OpenClaw, scripts, and future agents one shared place to store project context as ordinary files.

An AgentsFS instance is just a folder and git repository. Its root `AGENTS.md` teaches agents the contract: read before acting, write dense Markdown notes with `description:` frontmatter, use `[[wikilinks]]`, cite sources, improve existing notes instead of appending noise, reorganize the knowledge base as it grows, finish each unit of work with a short session note in the instance's session journal (`agent-journal/` by default — the directory whose `INDEX.md` declares `agentsfs_role: journal`), and commit useful changes.

## Why it helps

Agents do better work with better context. Context means information relevant to the problem: project decisions, source documents, emails, PDFs, web research, code conventions, customer calls, prior dead ends, people, organizations, and current state.

Without AgentsFS, useful context is scattered across chat history, vendor memory, project files, docs, and one-off transcripts. New agents often start cold and ask the user to re-teach the same project.

AgentsFS gives agents a canonical place to distill that raw context into durable working knowledge. Knowledge can compound across sessions and tools while staying inspectable by the human owner: plain files, local by default, editable with any editor, and versioned with git.

## Before setup: explain and ask

Do not run setup commands until the user answers the initial questions.

First, explain what will happen in plain language:

> I can set up AgentsFS so your AI agents have a durable project memory instead of starting from scratch each session. The usual setup creates a personal folder at `~/agentsfs`, connects this workspace to it by writing a small instruction block, then I will read the AgentsFS contract and help seed the first useful notes. The memory stays local unless you later ask me to set up backup or sync.

Then ask:

1. Should this memory be personal to you, or shared with everyone who uses this project repository?
   - Recommended for most people: personal memory at `~/agentsfs`. It stays outside the codebase and can be reused across projects.
   - Team-shared memory means an `agentsfs/` folder is committed into this repository, so everyone with the repo can see it.
2. Should I connect only this workspace for now, or also make AgentsFS available to future sessions of this AI tool?
   - Recommended: connect only this workspace first.
   - Global connection means future sessions of this harness may discover AgentsFS automatically.
3. Should the memory stay only on this computer for now, or do you want help setting up private backup or sync after setup?
   - Recommended: keep it local first, then add git sync only if the user wants it.

Wait for the user's answers. If the user is unsure, choose the recommended path and say why before proceeding.

## Setup steps after the user answers

Always run commands from the project the user wants connected.

### 1. Make sure `afs` exists

```sh
export PATH="$HOME/.local/bin:$PATH"
if command -v go >/dev/null 2>&1; then
  export PATH="$HOME/go/bin:$(go env GOPATH)/bin:$PATH"
fi

if ! command -v afs >/dev/null 2>&1 || ! afs help 2>/dev/null | grep -q "afs setup"; then
  mkdir -p "$HOME/.local/bin"
  if [ -n "${AGENTSFS_SOURCE:-}" ]; then
    (cd "$AGENTSFS_SOURCE" && GOBIN="$HOME/.local/bin" go install ./cmd/afs)
  else
    curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | AFS_INSTALL_DIR="$HOME/.local/bin" sh
  fi
fi

hash -r 2>/dev/null || true
command -v afs
afs version
```

If the installer cannot fetch the GitHub repo, ask the user for a local checkout path and set `AGENTSFS_SOURCE=/path/to/agentsfs`.

### 2. Create or connect the memory

For the recommended personal setup:

```sh
afs setup --yes
```

That creates or reuses `~/agentsfs` and writes a small connection block to this project's nearest `AGENTS.md` or `CLAUDE.md`. If neither exists, it creates `./AGENTS.md`.

For an existing AgentsFS at another path:

```sh
afs connect <path-to-agentsfs> --yes
```

For team-shared memory inside this repository, only after the user explicitly chose it:

```sh
afs init ./agentsfs --shared
afs connect ./agentsfs --yes
```

For global harness connection, only after the user explicitly chose it:

```sh
afs connect ~/agentsfs --global
```

If the harness cannot read `~/agentsfs`, tell the user to allowlist that path.

### 3. Read the contract before writing

After setup, read the root contract in full:

```sh
afs tree ~/agentsfs
sed -n '1,260p' ~/agentsfs/AGENTS.md
```

On a large memory, scope the tree to stay oriented: `afs tree ~/agentsfs/<dir>` shows one subtree and `--depth N` caps how deep it expands.

Follow that contract. It is the source of truth for how to read, write, link, reorganize, clean up, and commit knowledge.

### 4. Seed useful starter context

Ask the user for domain context, not folder design. Good questions:

- What should future agents never have to ask you again about this project?
- Which projects, people, organizations, systems, or decisions recur?
- Are there existing docs, emails, PDFs, tickets, notes, or code conventions I should distill?
- What is the current state, and what decisions or open questions matter most?

Then choose a simple starter structure yourself. Create `INDEX.md` files for directories, write dense notes with `description:` frontmatter, link recurring entities with `[[wikilinks]]`, cite sources, and prefer updating existing files over creating duplicates.

Do not ask the user to design the knowledge-base taxonomy. Own the structure, explain what you did, and reorganize as you learn more.

Finish by appending a session note to the session journal — `agent-journal/` by default; one dated file with a `description:` line, see its `INDEX.md` — then commit from the AgentsFS root:

```sh
cd ~/agentsfs
git status --short
git add -A .
git commit -m "Seed project memory"
```

If a git remote is configured, pull before working and push after committing. If no remote exists, keep the memory local unless the user asks for backup or sync.

If the user does want backup, sync, or a place to browse and share their memory, the **agentsfs Hub** is the turnkey option: `afs hub login` once, then `afs hub push` from the agentsfs root to link and upload it (repos are private by default). It stores real git plus Git LFS media objects, so `git clone` stays the exit ramp. An ordinary git remote (GitHub, etc.) works too. See `afs docs hub`.
