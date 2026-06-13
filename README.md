# agentsfs

**A portable, user-owned memory for AI agents — files + conventions + a thin toolkit, and nothing else.**

An agentsfs instance is a plain git repo. Knowledge lives in markdown with one-line `description:` frontmatter and `[[wikilinks]]`; any file type can live alongside it. A self-describing root `AGENTS.md` teaches any agent — Claude Code, Codex, anything — how to read, write, and maintain it. Your agents do the thinking; agentsfs makes what they learn survive across sessions, tools, and machines.

No server. No account. No LLM inside. `git clone` is the exit ramp.

## Agent quickstart

When a user asks you to set up agentsfs, do this from the project they want connected:

```sh
if command -v go >/dev/null 2>&1; then
  export PATH="$(go env GOPATH)/bin:$PATH"
fi
export PATH="$HOME/.local/bin:$PATH"
if ! command -v afs >/dev/null 2>&1 || ! afs help 2>/dev/null | grep -q "afs setup"; then
  if [ -n "${AGENTSFS_SOURCE:-}" ]; then
    (cd "$AGENTSFS_SOURCE" && go install ./cmd/afs)
  else
    curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | sh
  fi
fi
afs version
afs setup --yes
```

If the GitHub repo is still private and the curl installer returns 404, set `AGENTSFS_SOURCE=/path/to/agentsfs` to a local checkout and rerun the block.

Then read the connected filesystem's contract:

```sh
afs tree ~/agentsfs
sed -n '1,220p' ~/agentsfs/AGENTS.md
```

Seed it only after reading that contract. Ask the user what this memory is for, which people/projects/organizations recur, and what future sessions should never have to ask again. Then create a small starter structure with `INDEX.md` files, dense notes with `description:` frontmatter and `[[wikilinks]]`, and commit from the agentsfs root:

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
go install ./cmd/afs
```

Then verify:

```sh
afs version
```

Git LFS is optional; if it is missing, agentsfs still works and prints a note.

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
afs mcp          the same capabilities over MCP, for harnesses that can't shell out
```

All derived state lives in `.agentsfs/` (one SQLite file), is never committed, and rebuilds from the files with `afs reindex`.

## Docs

- [docs/setup.md](docs/setup.md) — agent and human setup instructions.
- [docs/releasing.md](docs/releasing.md) — packaged install and release process.
- [docs/agentsfs-source-of-truth.md](docs/agentsfs-source-of-truth.md) — what this is and why; the settled design decisions.
- [docs/execution-plan.md](docs/execution-plan.md) — how it's being built.
- [template/AGENTS.md](template/AGENTS.md) — the contract itself, as agents read it.

Status: pre-release, under active construction.
