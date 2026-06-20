# AgentsFS

**A shared filesystem for agent context — the place where useful knowledge compounds.**

Agents do their best work when they have high-quality context: the information relevant to the problem they are trying to solve. That context might come from the web, local files, email, PDFs, spreadsheets, chat messages, source code, or decisions made in a previous session. Today it usually lives in scattered places, with different shapes, different quality levels, and different rules for every tool.

AgentsFS gives agents one canonical place to turn that raw context into durable working knowledge. An agent can read from many sources, distill what matters, cite where it came from, link related ideas, clean up noise, and find the useful version later. Knowledge can compound instead of being rebuilt from scratch every time.

Agents also do not have a default shared filesystem for this. Claude Code, Codex, OpenClaw, scripts, and future tools may each have their own habits and storage surfaces. AgentsFS gives all of them the same simple contract: a folder, instructions, conventions, tools, and git history.

No server required. No account required for the open-source core. No LLM inside. `git clone` is the exit ramp.

## The layers

Layer 0: agents already know how to use the computer's filesystem. Claude Code, Codex, OpenClaw, Pi, scripts, and many other tools can read files, write files, search, and move things around.

Layer 1: AgentsFS adds prompts and a canonical file format. Agents are encouraged to retain knowledge by writing, recall knowledge by reading, and periodically clean up what they have saved. Markdown notes carry one-line `description:` frontmatter, use `[[wikilinks]]`, cite sources, and live in directories that explain themselves with `INDEX.md`. This makes knowledge easy for agents to parse and easy for humans to inspect. It also works well in existing tools like Obsidian because it is just files, YAML frontmatter, and wiki-style links.

Layer 2: AgentsFS adds an agent-friendly capability layer. The `afs` CLI and MCP server provide progressive disclosure with `afs tree`, health checks with `afs doctor`, search, backlinks, link-aware rename, reindexing, and more. These tools augment the agent's existing filesystem abilities; they do not replace the filesystem.

Layer 3: AgentsFS uses git for history. Commits, diffs, reviewable changes, and ordinary remote sync make the knowledge portable across machines and agents without inventing a new storage platform.

That is the whole primitive: slightly opinionated where the opinions matter, portable across operating systems and agent harnesses, and intentionally cut down to the smallest useful shape.

## What it is

An agentsfs instance is a plain git repo. Knowledge lives in markdown with one-line `description:` frontmatter and `[[wikilinks]]`; any file type can live alongside it. A self-describing root `AGENTS.md` teaches any agent — Claude Code, Codex, anything — how to read, write, and maintain it. Your agents do the thinking; agentsfs makes what they learn survive across sessions, tools, and machines.

## Agent quickstart

If `afs` is already installed in a fresh workspace, start here:

```sh
afs docs agent-start
```

Read it before running setup. It explains what AgentsFS is, why it helps, which questions to ask the user first, and the exact setup flow.

When a user asks you to set up agentsfs, do this from the project they want connected:

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
    AFS_INSTALL_DIR="$HOME/.local/bin" curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | sh
  fi
fi
hash -r 2>/dev/null || true
command -v afs
afs version
afs setup --yes
```

Agent setup installs to `~/.local/bin` because many agent shells inherit that path but do not read interactive shell profiles that add `~/go/bin`. If the GitHub repo is still private and the curl installer returns 404, set `AGENTSFS_SOURCE=/path/to/agentsfs` to a local checkout and rerun the block.

Then read the connected filesystem's contract:

```sh
afs tree ~/agentsfs
sed -n '1,220p' ~/agentsfs/AGENTS.md
```

Seed it only after reading that contract. Ask the user what this memory is for, which people/projects/organizations recur, and what future sessions should never have to ask again. Ask for domain context, not folder design: the agent should choose a small starter structure with `INDEX.md` files, dense notes with `description:` frontmatter and `[[wikilinks]]`, and commit from the agentsfs root:

```sh
cd ~/agentsfs
git status --short
git add -A .
git commit -m "Seed agentsfs"
```

Safety rules for agents:

- Prefer the personal shape: `~/agentsfs` outside the codebase, connected with `afs setup` or `afs connect`.
- Do not run `afs init ./agentsfs --shared` unless the user explicitly wants memory committed with this repo.
- Do not run `afs connect ~/agentsfs --global` unless the user explicitly wants every future session for that harness to know about this agentsfs.
- If the harness cannot read `~/agentsfs`, tell the user to allowlist that path.

## Human quickstart

### 1. Install `afs`

Fast path:

```sh
curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | sh
```

Before the first GitHub release exists, the installer falls back to a source build and needs Go + git.

Homebrew:

```sh
brew tap seekinggradient/agentsfs https://github.com/seekinggradient/agentsfs.git
brew install --HEAD seekinggradient/agentsfs/afs
```

Source fallback:

```sh
git clone https://github.com/seekinggradient/agentsfs.git
cd agentsfs
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" go install ./cmd/afs
```

Then verify:

```sh
command -v afs
afs version
```

Git LFS is optional; if it is missing, agentsfs still works and prints a note.

To uninstall the CLI later:

```sh
afs uninstall --dry-run
afs uninstall --yes
```

`afs uninstall` removes the installed CLI binary when it is in a user install directory. It never deletes `~/agentsfs`, any agentsfs repo, git history, or project-local connection blocks. If you installed with Homebrew, use `brew uninstall seekinggradient/agentsfs/afs`.

### 2. Connect a project to your personal agentsfs

Run this from the project where you want agents to remember durable context:

```sh
cd ~/code/myapp
afs setup --yes
```

That creates or reuses `~/agentsfs`, then adds a connection block to the project's nearest `AGENTS.md` or `CLAUDE.md` so future agents know where the filesystem lives.

The recommended shape is one personal agentsfs outside any codebase, shared across projects, never mixed into a repo's git history. `afs setup` is the friendly path: create or reuse that filesystem, then connect the current project to it. To connect another project later:

```sh
cd ~/code/another-project
afs connect ~/agentsfs --yes
```

The lower-level commands are deliberately boring:

```sh
afs init ~/agentsfs             # create an agentsfs at exactly this path
afs connect ~/agentsfs --global # connect global Claude/Codex config, if present
afs init ./agentsfs --shared    # team-shared memory committed with this repo
```

If `afs init` would create files inside a git repo, it refuses unless `--shared` is explicit. Personal memory should live outside the codebase; shared memory enters the codebase's history.

Then point any agent at it — or let the connection block do it — and work normally. See [docs/setup.md](docs/setup.md) for the full agent and human setup guide, [prompts/onboarding.md](prompts/onboarding.md) for the first session, and [prompts/gardening.md](prompts/gardening.md) for scheduled maintenance.

### Optional Git/GitHub backup and sync

agentsfs does not include a managed hosting service. The durable object is the folder itself: ordinary files in an ordinary git repo. If a user wants backup or cross-device sync, recommend connecting that repo to a private GitHub repository, GitLab repository, or self-hosted git remote.

Agents should make this human-sized before touching remotes. Ask in this order:

- Do you want this agentsfs backed up or synced across computers?
- Do you know what Git is?
- Do you have a GitHub account?

If the user wants sync and has GitHub, help them create an empty private repository and connect it:

```sh
cd ~/agentsfs
git remote add origin git@github.com:<user>/<repo>.git
git branch -M main
git push -u origin main
```

On another machine, restore it with plain git, then connect projects normally:

```sh
git clone git@github.com:<user>/<repo>.git ~/agentsfs
cd ~/code/myapp
afs connect ~/agentsfs --yes
```

If the user does not know Git or does not have GitHub, explain the minimum: Git records history inside the folder; GitHub can hold a private online copy for backup and sync. Guide them through account creation and repository setup only with consent. Do not store GitHub tokens or passwords in the agentsfs repo.

## Skills (Claude Code / Agent Skills format)

The same behaviors, packaged as installable skills — `prompts/` stays the harness-neutral canonical text, `skills/` is the skill-native wrapper:

```sh
cp -R skills/agentsfs-* ~/.claude/skills/    # personal; or a project's .claude/skills/
```

- `agentsfs-setup` — create an agentsfs, connect projects, seed the first knowledge
- `agentsfs-remember` — "remember this": save conversation knowledge per the contract
- `agentsfs-garden` — doctor-driven maintenance and consolidation

## The toolkit

The contract works with zero tooling (`ls`, `grep`, git). The CLI adds what plain tools do poorly:

```
afs tree         the whole tree with descriptions and freshness — one-call orientation
afs search       ranked full-text search; --semantic with an embedding key (optional)
afs backlinks    every [[wikilink]] pointing at a file
afs rename       move a file and rewrite all links to it
afs doctor       deterministic health check; the gardener's worklist
afs docs         bundled AgentsFS docs from any workspace
afs mcp          the same capabilities over MCP, for harnesses that can't shell out
afs uninstall    remove the local CLI/config without deleting agentsfs data
```

All derived state lives in `.agentsfs/` (one SQLite file), is never committed, and rebuilds from the files with `afs reindex`.

## Docs

- [docs/agent-start.md](docs/agent-start.md) — agent-facing primer for fresh workspaces.
- [docs/setup.md](docs/setup.md) — agent and human setup instructions.
- [docs/releasing.md](docs/releasing.md) — packaged install and release process.
- [docs/agentsfs-source-of-truth.md](docs/agentsfs-source-of-truth.md) — what this is and why; the settled design decisions.
- [docs/execution-plan.md](docs/execution-plan.md) — how it's being built.
- [template/AGENTS.md](template/AGENTS.md) — the contract itself, as agents read it.

Status: pre-release, under active construction.
